/*
 * QEMU RTL8168G (Realtek 8168/8111) NIC emulation.
 *
 * Minimal emulation for testing firmware loading in the Linux r8169 driver.
 * TxConfig identifies as RTL8168GU (VER_42), which requests firmware
 * rtl_nic/rtl8168g-2.fw (or rtl8168g-3.fw on newer kernels).
 *
 * Firmware gate:
 *   fw_loaded starts TRUE so iPXE can PXE-boot through the NIC.  iPXE
 *   uses PHYAR (0x60) for PHY access, not GPHY_OCP (0xb8).  When the
 *   Linux r8169 driver takes over it writes GPHY_OCP — the first such
 *   write sets fw_loaded=false and resets the PHY write counter, killing
 *   the link.  Subsequent GPHY_OCP writes (firmware loading) re-enable
 *   it once the threshold is reached.
 *
 *   Without firmware → link stays down → installer has no network.
 *   With firmware    → link comes back → installer works.
 *
 * SPDX-License-Identifier: GPL-2.0-or-later
 */

#include "qemu/osdep.h"
#include "hw/pci/pci_device.h"
#include "hw/qdev-properties.h"
#include "net/net.h"
#include "net/queue.h"
#include "qemu/module.h"
#include "qom/object.h"

/* ── PCI IDs ─────────────────────────────────────────────────── */

#define RTL8168_PCI_VENDOR_ID    0x10ec
#define RTL8168_PCI_DEVICE_ID    0x8168
#define RTL8168_PCI_REVISION     0x15  /* RTL8168GU */

/* ── Register offsets (from r8169_main.c) ────────────────────── */

enum {
    REG_MAC0             = 0x00,
    REG_MAC4             = 0x04,
    REG_MAR0             = 0x08,
    REG_MAR4             = 0x0c,
    REG_COUNTERADDR_LO   = 0x10,
    REG_TXDESC_LO        = 0x20,
    REG_TXDESC_HI        = 0x24,
    REG_CHIPCMD          = 0x37,
    REG_TXPOLL           = 0x38,
    REG_INTRMASK         = 0x3c,
    REG_INTRSTATUS       = 0x3e,
    REG_TXCONFIG         = 0x40,
    REG_RXCONFIG         = 0x44,
    REG_CFG9346          = 0x50,
    REG_CONFIG0          = 0x51,
    REG_CONFIG5          = 0x56,
    REG_PHYAR            = 0x60,
    REG_CSIDR            = 0x64,
    REG_CSIAR            = 0x68,
    REG_PHYSTATUS        = 0x6c,
    REG_ERIDR            = 0x70,
    REG_ERIAR            = 0x74,
    REG_EPHYAR           = 0x80,
    REG_OCPDR            = 0xb0,
    REG_OCPAR            = 0xb4,
    REG_GPHY_OCP         = 0xb8,
    REG_MCU              = 0xd3,
    REG_RXMAXSIZE        = 0xda,
    REG_CPLUSCMD         = 0xe0,
    REG_INTRMITIGATE     = 0xe2,
    REG_RXDESC_LO        = 0xe4,
    REG_RXDESC_HI        = 0xe8,
    REG_MAXTXPKTSIZE     = 0xec,
};

/* ── Register bits ───────────────────────────────────────────── */

#define CHIPCMD_TE          BIT(2)
#define CHIPCMD_RE          BIT(3)
#define CHIPCMD_RST         BIT(4)

#define TXPOLL_HPQ          BIT(7)
#define TXPOLL_NPQ          BIT(6)

#define CFG9346_UNLOCK      0xc0

#define PHYAR_FLAG          BIT(31)
#define ERIAR_FLAG          BIT(31)
#define CSIAR_FLAG          BIT(31)
#define EPHYAR_FLAG         BIT(31)

#define MCU_READY_BITS      (BIT(5) | BIT(4) | BIT(1))  /* TX_EMPTY|RX_EMPTY|LINK_LIST_RDY */

#define PHYSTATUS_LINK      BIT(1)
#define PHYSTATUS_FULLDUP   BIT(0)
#define PHYSTATUS_1000MF    BIT(4)
#define PHYSTATUS_TXFLOW    BIT(2)
#define PHYSTATUS_RXFLOW    BIT(3)

#define INTR_ROK            BIT(0)
#define INTR_TOK            BIT(2)
#define INTR_RDU            BIT(4)
#define INTR_LINK_CHG       BIT(5)

/*
 * TxConfig: r8169 extracts XID = (TxConfig >> 20) & 0xfcf, then matches
 * { mask=0x7cf, val=0x509 } → RTL_GIGA_MAC_VER_42 → rtl_nic/rtl8168g-2.fw.
 * TXCFG_EMPTY (bit 11) satisfies the rtl_txcfg_empty_cond poll.
 */
#define TXCONFIG_VER42      ((0x509u << 20) | BIT(11))

/* ── Descriptor format ───────────────────────────────────────── */

#define DESC_OWN            BIT(31)
#define DESC_EOR            BIT(30)
#define DESC_FS             BIT(29)
#define DESC_LS             BIT(28)
#define DESC_LEN_MASK       0x3fff
#define NUM_TX_DESC         256
#define NUM_RX_DESC         256

/*
 * Firmware detection threshold.  Firmware loading writes many registers
 * via GPHY_OCP + ERIAR; normal init without firmware does fewer.
 */
#define FW_PHY_WRITE_THRESHOLD  30

/* ── Device state ────────────────────────────────────────────── */

typedef struct RTL8168Desc {
    uint32_t opts1;
    uint32_t opts2;
    uint32_t addr_lo;
    uint32_t addr_hi;
} RTL8168Desc;

#define TYPE_RTL8168 "rtl8168"
OBJECT_DECLARE_SIMPLE_TYPE(RTL8168State, RTL8168)

struct RTL8168State {
    PCIDevice parent_obj;

    MemoryRegion mmio;
    MemoryRegion io;
    uint8_t regs[0x100];

    NICState *nic;
    NICConf conf;

    uint64_t tx_desc_addr;
    uint64_t rx_desc_addr;
    uint32_t tx_cur;
    uint32_t rx_cur;

    uint16_t intr_mask;
    uint16_t intr_status;

    uint16_t phy_regs[32];
    uint32_t phy_write_count;
    bool fw_loaded;
    bool gphy_seen;       /* first GPHY_OCP write seen (Linux r8169 active) */
    bool link_chg_pending; /* deferred LINK_CHG for delivery after IMR enable */

    bool cfg_unlocked;
    uint8_t chip_cmd;
};

/* ── Helpers ─────────────────────────────────────────────────── */

static void rtl8168_update_irq(RTL8168State *s)
{
    pci_set_irq(&s->parent_obj, !!(s->intr_status & s->intr_mask));
}

static void rtl8168_set_intr(RTL8168State *s, uint16_t bits)
{
    s->intr_status |= bits;
    rtl8168_update_irq(s);
}

static void rtl8168_check_fw(RTL8168State *s)
{
    if (!s->fw_loaded && s->phy_write_count >= FW_PHY_WRITE_THRESHOLD) {
        s->fw_loaded = true;
        s->link_chg_pending = true;
    }
}

static uint8_t rtl8168_phystatus(RTL8168State *s)
{
    /* Always report link up so the r8169 driver brings up the
     * interface and attempts firmware loading.  TX/RX are still
     * gated by fw_loaded — without firmware the NIC has "link"
     * but cannot send or receive, so the installer has no network. */
    return PHYSTATUS_LINK | PHYSTATUS_FULLDUP | PHYSTATUS_1000MF |
           PHYSTATUS_TXFLOW | PHYSTATUS_RXFLOW;
}

/* ── TX ──────────────────────────────────────────────────────── */

static void rtl8168_tx(RTL8168State *s)
{
    if (!(s->chip_cmd & CHIPCMD_TE) || !s->fw_loaded || !s->tx_desc_addr) {
        return;
    }

    for (int i = 0; i < NUM_TX_DESC; i++) {
        RTL8168Desc desc;
        hwaddr daddr = s->tx_desc_addr + s->tx_cur * sizeof(desc);

        pci_dma_read(&s->parent_obj, daddr, &desc, sizeof(desc));
        desc.opts1 = le32_to_cpu(desc.opts1);
        desc.addr_lo = le32_to_cpu(desc.addr_lo);
        desc.addr_hi = le32_to_cpu(desc.addr_hi);

        if (!(desc.opts1 & DESC_OWN)) {
            break;
        }

        uint32_t len = desc.opts1 & DESC_LEN_MASK;
        bool eor = desc.opts1 & DESC_EOR;

        if (len > 0 && len <= 9000) {
            uint8_t buf[9000];
            hwaddr baddr = ((uint64_t)desc.addr_hi << 32) | desc.addr_lo;
            pci_dma_read(&s->parent_obj, baddr, buf, len);
            qemu_send_packet(qemu_get_queue(s->nic), buf, len);
        }

        /* Clear OWN, keep EOR */
        uint32_t new_opts1 = cpu_to_le32(eor ? DESC_EOR : 0);
        pci_dma_write(&s->parent_obj, daddr, &new_opts1, 4);

        s->tx_cur = eor ? 0 : (s->tx_cur + 1) % NUM_TX_DESC;
    }

    rtl8168_set_intr(s, INTR_TOK);
}

/* ── RX ──────────────────────────────────────────────────────── */

static bool rtl8168_can_receive(NetClientState *nc)
{
    RTL8168State *s = qemu_get_nic_opaque(nc);
    return (s->chip_cmd & CHIPCMD_RE) && s->fw_loaded && s->rx_desc_addr;
}

static ssize_t rtl8168_receive(NetClientState *nc,
                               const uint8_t *buf, size_t size)
{
    RTL8168State *s = qemu_get_nic_opaque(nc);

    if (!rtl8168_can_receive(nc)) {
        return -1;
    }

    RTL8168Desc desc;
    hwaddr daddr = s->rx_desc_addr + s->rx_cur * sizeof(desc);

    pci_dma_read(&s->parent_obj, daddr, &desc, sizeof(desc));
    desc.opts1 = le32_to_cpu(desc.opts1);
    desc.addr_lo = le32_to_cpu(desc.addr_lo);
    desc.addr_hi = le32_to_cpu(desc.addr_hi);

    if (!(desc.opts1 & DESC_OWN)) {
        rtl8168_set_intr(s, INTR_RDU);
        return 0;
    }

    bool eor = desc.opts1 & DESC_EOR;
    hwaddr baddr = ((uint64_t)desc.addr_hi << 32) | desc.addr_lo;
    pci_dma_write(&s->parent_obj, baddr, buf, size);

    /* Report size + 4: real hardware includes CRC in the length and
     * iPXE subtracts 4 (strip CRC) when processing received packets. */
    uint32_t reported = size + 4;
    uint32_t new_opts1 = cpu_to_le32(
        (eor ? DESC_EOR : 0) | DESC_FS | DESC_LS | (reported & DESC_LEN_MASK));
    pci_dma_write(&s->parent_obj, daddr, &new_opts1, 4);

    s->rx_cur = eor ? 0 : (s->rx_cur + 1) % NUM_RX_DESC;
    rtl8168_set_intr(s, INTR_ROK);
    return size;
}

/* ── MMIO read ───────────────────────────────────────────────── */

static uint64_t rtl8168_mmio_read(void *opaque, hwaddr addr, unsigned size)
{
    RTL8168State *s = opaque;

    switch (addr) {
    case REG_MAC0:
        return ldl_le_p(s->conf.macaddr.a);
    case REG_MAC4:
        return lduw_le_p(s->conf.macaddr.a + 4);
    case REG_CHIPCMD:
        return s->chip_cmd;
    case REG_TXCONFIG:
        return TXCONFIG_VER42;
    case REG_RXCONFIG:
        return 0x0000e70e;
    case REG_INTRMASK:
        return s->intr_mask;
    case REG_INTRSTATUS:
        return s->intr_status;
    case REG_PHYAR: {
        /* iPXE uses PHYAR for PHY access.
         * Write (bit31=1) → polls wait_low → return flag clear + stored data.
         * Read  (bit31=0) → polls wait_high → return flag set + PHY data. */
        uint32_t v = ldl_le_p(&s->regs[REG_PHYAR]);
        if (v & PHYAR_FLAG) {
            return v & ~PHYAR_FLAG;
        }
        uint8_t reg = (v >> 16) & 0x1f;
        return PHYAR_FLAG | s->phy_regs[reg];
    }
    case REG_PHYSTATUS:
        return rtl8168_phystatus(s);
    case REG_CFG9346:
        return s->cfg_unlocked ? CFG9346_UNLOCK : 0;
    case REG_CONFIG0 ... REG_CONFIG5:
        return s->regs[addr];
    case REG_CPLUSCMD:
        return 0x2060;
    case REG_RXMAXSIZE:
        return lduw_le_p(&s->regs[REG_RXMAXSIZE]);
    case REG_MAXTXPKTSIZE:
        return s->regs[REG_MAXTXPKTSIZE];
    case REG_INTRMITIGATE:
        return lduw_le_p(&s->regs[REG_INTRMITIGATE]);
    case REG_TXDESC_LO:
        return (uint32_t)s->tx_desc_addr;
    case REG_TXDESC_HI:
        return (uint32_t)(s->tx_desc_addr >> 32);
    case REG_RXDESC_LO:
        return (uint32_t)s->rx_desc_addr;
    case REG_RXDESC_HI:
        return (uint32_t)(s->rx_desc_addr >> 32);
    case REG_OCPDR:
        return ldl_le_p(&s->regs[REG_OCPDR]) & ~0x80000000u;
    case REG_OCPAR:
        return ldl_le_p(&s->regs[REG_OCPAR]) | 0x80000000u;
    case REG_GPHY_OCP: {
        /*
         * GPHY OCP indirect access.
         *
         * Write cmd (bit31=1) → driver polls wait_low → return flag clear.
         * Read cmd  (bit31=0) → driver polls wait_high → return flag set.
         *
         * OCP address = (val >> 15) & 0xffff.  Standard PHY regs at 0xa400+.
         */
        uint32_t v = ldl_le_p(&s->regs[REG_GPHY_OCP]);
        if (v & 0x80000000u) {
            return v & ~0x80000000u;
        }
        uint32_t ocp_reg = (v >> 15) & 0xffff;
        uint16_t data = 0;
        if (ocp_reg >= 0xa400 && ocp_reg < 0xa400 + 64) {
            int phyreg = (ocp_reg - 0xa400) / 2;
            if (phyreg < 32) {
                data = s->phy_regs[phyreg];
            }
            /* PHY INSR (reg 0x13): report link-change event when the
             * firmware gate is open.  The RTL8211B handle_interrupt
             * checks 0x6400 (link change + AN complete) before
             * triggering phylib to re-check the link. */
            if (phyreg == 0x13 && s->fw_loaded && s->gphy_seen) {
                data |= 0x6400;
            }
        }
        return 0x80000000u | data;
    }
    case REG_MCU:
        return MCU_READY_BITS;
    case REG_ERIAR: {
        /* Same protocol as PHYAR: write(flag=1)→poll until flag=0,
         * read(flag=0)→poll until flag=1. */
        uint32_t v = ldl_le_p(&s->regs[REG_ERIAR]);
        if (v & ERIAR_FLAG) return v & ~ERIAR_FLAG;
        return ERIAR_FLAG | ldl_le_p(&s->regs[REG_ERIDR]);
    }
    case REG_ERIDR:
        return ldl_le_p(&s->regs[REG_ERIDR]);
    case REG_CSIAR: {
        uint32_t v = ldl_le_p(&s->regs[REG_CSIAR]);
        if (v & CSIAR_FLAG) return v & ~CSIAR_FLAG;
        return CSIAR_FLAG;
    }
    case REG_EPHYAR: {
        uint32_t v = ldl_le_p(&s->regs[REG_EPHYAR]);
        if (v & EPHYAR_FLAG) return v & ~EPHYAR_FLAG;
        return EPHYAR_FLAG;
    }
    default:
        if (addr < sizeof(s->regs)) {
            switch (size) {
            case 1: return s->regs[addr];
            case 2: return lduw_le_p(&s->regs[addr]);
            case 4: return ldl_le_p(&s->regs[addr]);
            }
        }
        return 0;
    }
}

/* ── MMIO write ──────────────────────────────────────────────── */

static void rtl8168_mmio_write(void *opaque, hwaddr addr,
                               uint64_t val, unsigned size)
{
    RTL8168State *s = opaque;

    switch (addr) {
    case REG_COUNTERADDR_LO:
        /* Counter dump: clear CounterDump(0x8) + CounterReset(0x1)
         * immediately — no actual DMA counter dump is performed. */
        stl_le_p(&s->regs[addr], val & ~0x9u);
        break;
    case REG_MAC0:
        if (s->cfg_unlocked) {
            stl_le_p(s->conf.macaddr.a, val);
            stl_le_p(&s->regs[0], val);
        }
        break;
    case REG_MAC4:
        if (s->cfg_unlocked) {
            stw_le_p(s->conf.macaddr.a + 4, val);
            stw_le_p(&s->regs[4], val);
        }
        break;
    case REG_MAR0:
    case REG_MAR4:
        stl_le_p(&s->regs[addr], val);
        break;
    case REG_CHIPCMD:
        if (val & CHIPCMD_RST) {
            s->chip_cmd = 0;
            s->intr_status = 0;
            s->intr_mask = 0;
            s->tx_cur = 0;
            s->rx_cur = 0;
            rtl8168_update_irq(s);
        } else {
            s->chip_cmd = val & (CHIPCMD_TE | CHIPCMD_RE);
        }
        break;
    case REG_TXPOLL:
        if (val & (TXPOLL_HPQ | TXPOLL_NPQ)) {
            rtl8168_tx(s);
            qemu_flush_queued_packets(qemu_get_queue(s->nic));
        }
        break;
    case REG_INTRMASK:
        s->intr_mask = val;
        if ((val & INTR_LINK_CHG) && s->link_chg_pending) {
            s->link_chg_pending = false;
            rtl8168_set_intr(s, INTR_LINK_CHG);
        }
        rtl8168_update_irq(s);
        break;
    case REG_INTRSTATUS:
        s->intr_status &= ~val;
        rtl8168_update_irq(s);
        break;
    case REG_TXCONFIG:
        break;  /* read-only version bits */
    case REG_RXCONFIG:
        stl_le_p(&s->regs[REG_RXCONFIG], val);
        break;
    case REG_CFG9346:
        s->cfg_unlocked = ((val & 0xc0) == CFG9346_UNLOCK);
        break;
    case REG_CONFIG0 ... REG_CONFIG5:
        if (s->cfg_unlocked) s->regs[addr] = val;
        break;
    case REG_PHYAR: {
        /*
         * iPXE WRITE: writes flag=1, polls until flag=0.
         * iPXE READ:  writes flag=0, polls until flag=1.
         *
         * Store raw value so the read handler can distinguish:
         *   flag stored → was a write → return without flag (complete)
         *   no flag     → was a read  → return with flag + PHY data
         *
         * PHYAR writes are from iPXE only (Linux uses GPHY_OCP for
         * RTL8168G), so they do NOT count toward firmware detection.
         */
        uint32_t v = (uint32_t)val;
        stl_le_p(&s->regs[REG_PHYAR], val);
        if (v & PHYAR_FLAG) {
            uint8_t reg = (v >> 16) & 0x1f;
            uint16_t data = v & 0xffff;
            s->phy_regs[reg] = data;
            /* Auto-clear BMCR reset (bit 15) and AN restart (bit 9) —
             * real PHY clears these after processing the command. */
            if (reg == 0) {
                s->phy_regs[0] = data & ~0x8200;
            }
        }
        break;
    }
    case REG_RXMAXSIZE:
        stw_le_p(&s->regs[REG_RXMAXSIZE], val);
        break;
    case REG_CPLUSCMD:
        stw_le_p(&s->regs[REG_CPLUSCMD], val);
        break;
    case REG_INTRMITIGATE:
        stw_le_p(&s->regs[REG_INTRMITIGATE], val);
        break;
    case REG_TXDESC_LO:
        s->tx_desc_addr = (s->tx_desc_addr & ~0xffffffffULL) | val;
        s->tx_cur = 0;
        break;
    case REG_TXDESC_HI:
        s->tx_desc_addr = (s->tx_desc_addr & 0xffffffffULL) | ((uint64_t)val << 32);
        break;
    case REG_RXDESC_LO:
        s->rx_desc_addr = (s->rx_desc_addr & ~0xffffffffULL) | val;
        s->rx_cur = 0;
        break;
    case REG_RXDESC_HI:
        s->rx_desc_addr = (s->rx_desc_addr & 0xffffffffULL) | ((uint64_t)val << 32);
        break;
    case REG_MAXTXPKTSIZE:
        s->regs[REG_MAXTXPKTSIZE] = val;
        break;
    case REG_OCPDR:
    case REG_ERIDR:
    case REG_CSIAR:
    case REG_CSIDR:
    case REG_EPHYAR:
        stl_le_p(&s->regs[addr], val);
        break;
    case REG_OCPAR:
        stl_le_p(&s->regs[addr], val);
        if ((val & 0x80000000u) && s->gphy_seen) {
            s->phy_write_count++;
            rtl8168_check_fw(s);
        }
        break;
    case REG_ERIAR:
        stl_le_p(&s->regs[addr], val);
        if ((val & ERIAR_FLAG) && s->gphy_seen) {
            s->phy_write_count++;
            rtl8168_check_fw(s);
        }
        break;
    case REG_GPHY_OCP:
        stl_le_p(&s->regs[REG_GPHY_OCP], val);
        if (val & 0x80000000u) {
            if (!s->gphy_seen) {
                /* First GPHY_OCP write: Linux r8169 is driving the NIC.
                 * Kill link until firmware PHY writes re-enable it.
                 * Reset counter so iPXE's earlier PHYAR writes don't
                 * interfere with firmware detection. */
                s->gphy_seen = true;
                s->fw_loaded = false;
                s->phy_write_count = 0;
            }
            uint32_t ocp_reg = (val >> 15) & 0xffff;
            if (ocp_reg >= 0xa400 && ocp_reg < 0xa400 + 64) {
                int phyreg = (ocp_reg - 0xa400) / 2;
                if (phyreg < 32) {
                    uint16_t data = val & 0xffff;
                    s->phy_regs[phyreg] = data;
                    if (phyreg == 0) {
                        s->phy_regs[0] = data & ~0x8200;
                    }
                }
            }
            s->phy_write_count++;
            rtl8168_check_fw(s);
        }
        break;
    default:
        if (addr < sizeof(s->regs)) {
            switch (size) {
            case 1: s->regs[addr] = val; break;
            case 2: stw_le_p(&s->regs[addr], val); break;
            case 4: stl_le_p(&s->regs[addr], val); break;
            }
        }
        break;
    }
}

static const MemoryRegionOps rtl8168_mmio_ops = {
    .read = rtl8168_mmio_read,
    .write = rtl8168_mmio_write,
    .endianness = DEVICE_LITTLE_ENDIAN,
    .impl = { .min_access_size = 1, .max_access_size = 4 },
};

/* ── NIC backend ─────────────────────────────────────────────── */

static NetClientInfo rtl8168_net_info = {
    .type = NET_CLIENT_DRIVER_NIC,
    .size = sizeof(NICState),
    .can_receive = rtl8168_can_receive,
    .receive = rtl8168_receive,
};

/* ── PHY register defaults ───────────────────────────────────── */

static void rtl8168_init_phy(RTL8168State *s)
{
    memset(s->phy_regs, 0, sizeof(s->phy_regs));
    s->phy_regs[0]  = 0x1140;  /* BMCR: autoneg, 1Gbps */
    s->phy_regs[1]  = 0x796d;  /* BMSR: link, autoneg capable */
    s->phy_regs[2]  = 0x001c;  /* PHYSID1: Realtek OUI */
    s->phy_regs[3]  = 0xc912;  /* PHYSID2: RTL8168G */
    s->phy_regs[4]  = 0x01e1;  /* ADVERTISE */
    s->phy_regs[5]  = 0xc5e1;  /* LPA */
    s->phy_regs[6]  = 0x000f;  /* EXPANSION */
    s->phy_regs[9]  = 0x0200;  /* CTRL1000 */
    s->phy_regs[10] = 0x3c00;  /* STAT1000 */
    s->phy_regs[15] = 0x3000;  /* ESTATUS */
}

/* ── PCI lifecycle ───────────────────────────────────────────── */

static void rtl8168_reset(DeviceState *dev)
{
    RTL8168State *s = RTL8168(dev);

    memset(s->regs, 0, sizeof(s->regs));
    /* Copy MAC into register space so byte-level reads (iPXE) work */
    memcpy(s->regs, s->conf.macaddr.a, 6);
    s->chip_cmd = 0;
    s->intr_mask = 0;
    s->intr_status = 0;
    s->tx_desc_addr = 0;
    s->rx_desc_addr = 0;
    s->tx_cur = 0;
    s->rx_cur = 0;
    s->fw_loaded = true;  /* allow iPXE PXE boot; GPHY_OCP write disables */
    s->phy_write_count = 0;
    s->gphy_seen = false;
    s->link_chg_pending = false;
    s->cfg_unlocked = false;

    rtl8168_init_phy(s);
}

static void rtl8168_instance_init(Object *obj)
{
    RTL8168State *s = RTL8168(obj);

    device_add_bootindex_property(obj, &s->conf.bootindex,
                                  "bootindex", "/ethernet-phy@0",
                                  DEVICE(obj));
}

static void rtl8168_realize(PCIDevice *pci_dev, Error **errp)
{
    RTL8168State *s = RTL8168(pci_dev);

    pci_dev->config[PCI_INTERRUPT_PIN] = 1;

    memory_region_init_io(&s->mmio, OBJECT(s), &rtl8168_mmio_ops, s,
                          "rtl8168-mmio", 0x100);
    /* BAR 0 = I/O, BAR 2 = MMIO (same as real RTL8168) */
    memory_region_init_io(&s->io, OBJECT(s), &rtl8168_mmio_ops, s,
                          "rtl8168-io", 0x100);
    pci_register_bar(pci_dev, 0, PCI_BASE_ADDRESS_SPACE_IO, &s->io);
    pci_register_bar(pci_dev, 2, PCI_BASE_ADDRESS_SPACE_MEMORY, &s->mmio);

    s->nic = qemu_new_nic(&rtl8168_net_info, &s->conf,
                           object_get_typename(OBJECT(s)),
                           pci_dev->qdev.id,
                           &pci_dev->qdev.mem_reentrancy_guard, s);
    qemu_format_nic_info_str(qemu_get_queue(s->nic), s->conf.macaddr.a);
}

static void rtl8168_exit(PCIDevice *pci_dev)
{
    qemu_del_nic(RTL8168(pci_dev)->nic);
}

/* ── Type registration ───────────────────────────────────────── */

static Property rtl8168_properties[] = {
    DEFINE_NIC_PROPERTIES(RTL8168State, conf),
    DEFINE_PROP_END_OF_LIST(),
};

static void rtl8168_class_init(ObjectClass *klass, void *data)
{
    DeviceClass *dc = DEVICE_CLASS(klass);
    PCIDeviceClass *k = PCI_DEVICE_CLASS(klass);

    k->realize = rtl8168_realize;
    k->exit = rtl8168_exit;
    k->vendor_id = RTL8168_PCI_VENDOR_ID;
    k->device_id = RTL8168_PCI_DEVICE_ID;
    k->revision = RTL8168_PCI_REVISION;
    k->class_id = PCI_CLASS_NETWORK_ETHERNET;

    dc->reset = rtl8168_reset;
    dc->desc = "RTL8168G Gigabit Ethernet (firmware-gated)";
    device_class_set_props(dc, rtl8168_properties);
    set_bit(DEVICE_CATEGORY_NETWORK, dc->categories);
}

static const TypeInfo rtl8168_type_info = {
    .name          = TYPE_RTL8168,
    .parent        = TYPE_PCI_DEVICE,
    .instance_size = sizeof(RTL8168State),
    .instance_init = rtl8168_instance_init,
    .class_init    = rtl8168_class_init,
    .interfaces    = (InterfaceInfo[]) {
        { INTERFACE_CONVENTIONAL_PCI_DEVICE },
        { },
    },
};

static void rtl8168_register_types(void)
{
    type_register_static(&rtl8168_type_info);
}

type_init(rtl8168_register_types)
