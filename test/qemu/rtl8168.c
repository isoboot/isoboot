/*
 * QEMU RTL8168G (Realtek 8168/8111) NIC emulation.
 *
 * Minimal emulation of an RTL8168G NIC for testing firmware loading in
 * the Linux r8169 driver. The emulated device intentionally requires
 * firmware: link status stays down until the driver applies PHY firmware
 * via the PHYAR register, mimicking real hardware that requires
 * rtl_nic/rtl8168g-2.fw for the PHY to establish link.
 *
 * Without firmware the NIC appears dead (link down), so the OS installer
 * cannot use the network. With firmware the link comes up and traffic
 * flows normally through standard TX/RX descriptor rings.
 *
 * SPDX-License-Identifier: GPL-2.0-or-later
 */

#include "qemu/osdep.h"
#include "hw/pci/pci_device.h"
#include "hw/qdev-properties.h"
#include "hw/pci/msi.h"
#include "net/net.h"
#include "net/eth.h"
#include "qemu/module.h"
#include "qemu/timer.h"
#include "qom/object.h"

/* PCI IDs */
#define RTL8168_PCI_VENDOR_ID    0x10ec
#define RTL8168_PCI_DEVICE_ID    0x8168
#define RTL8168_PCI_REVISION     0x15  /* RTL8168GU */

/* Register offsets (from r8169_main.c) */
enum {
    REG_MAC0             = 0x00,
    REG_MAC4             = 0x04,
    REG_MAR0             = 0x08,
    REG_MAR4             = 0x0c,
    REG_DTCCR            = 0x10,  /* DumpTally counter */
    REG_DTCCR_HI         = 0x14,
    REG_TXDESC_LO        = 0x20,
    REG_TXDESC_HI        = 0x24,
    REG_TX_HDESC_LO      = 0x28,
    REG_TX_HDESC_HI      = 0x2c,
    REG_FLASH            = 0x30,
    REG_ERSR             = 0x36,
    REG_CHIPCMD          = 0x37,
    REG_TXPOLL           = 0x38,
    REG_INTRMASK         = 0x3c,
    REG_INTRSTATUS       = 0x3e,
    REG_TXCONFIG         = 0x40,
    REG_RXCONFIG         = 0x44,
    REG_TCTR             = 0x48,
    REG_CFG9346          = 0x50,
    REG_CONFIG0          = 0x51,
    REG_CONFIG1          = 0x52,
    REG_CONFIG2          = 0x53,
    REG_CONFIG3          = 0x54,
    REG_CONFIG4          = 0x55,
    REG_CONFIG5          = 0x56,
    REG_PHYAR            = 0x60,
    REG_CSIDR            = 0x64,
    REG_CSIAR            = 0x68,
    REG_PHYSTATUS        = 0x6c,
    REG_ERIDR            = 0x70,
    REG_ERIAR            = 0x74,
    REG_OCPDR            = 0xb0,  /* OCP GPHY access data */
    REG_OCPAR            = 0xb4,  /* OCP GPHY access address */
    REG_GPHY_OCP         = 0xb8,  /* GPHY OCP access */
    REG_MCU              = 0xd3,
    REG_RXMAXSIZE        = 0xda,
    REG_CPLUSCMD         = 0xe0,
    REG_INTRMITIGATE     = 0xe2,
    REG_RXDESC_LO        = 0xe4,
    REG_RXDESC_HI        = 0xe8,
    REG_MAXTXPKTSIZE     = 0xec,
    REG_FUNCEVENT        = 0xf0,
    REG_FUNCEVENTMASK    = 0xf4,
    REG_FUNCPRESETSTATE  = 0xf8,
    REG_FORCEEVENT       = 0xfc,
};

/* Register bits */
#define CHIPCMD_TE          BIT(2)  /* TX enable */
#define CHIPCMD_RE          BIT(3)  /* RX enable */
#define CHIPCMD_RST         BIT(4)  /* Reset */

#define TXPOLL_HPQ          BIT(7)  /* High-prio poll */
#define TXPOLL_NPQ          BIT(6)  /* Normal-prio poll */

#define CFG9346_UNLOCK      0xc0
#define CFG9346_LOCK        0x00

#define PHYAR_FLAG          BIT(31) /* R/W flag */
#define ERIAR_FLAG          BIT(31) /* ERI access done */
#define CSIAR_FLAG          BIT(31) /* CSI access done */

/* MCU register bits */
#define MCU_TX_EMPTY        BIT(5)
#define MCU_RX_EMPTY        BIT(4)
#define MCU_LINK_LIST_RDY   BIT(1)
#define MCU_READY_BITS      (MCU_TX_EMPTY | MCU_RX_EMPTY | MCU_LINK_LIST_RDY)

/* PHYstatus bits */
#define PHYSTATUS_FULLDUP   BIT(0)
#define PHYSTATUS_LINK      BIT(1)
#define PHYSTATUS_TXFLOW    BIT(2)
#define PHYSTATUS_RXFLOW    BIT(3)
#define PHYSTATUS_1000MF    BIT(4)
#define PHYSTATUS_100M      BIT(5)
#define PHYSTATUS_10M       BIT(6)

/* Interrupt bits */
#define INTR_ROK            BIT(0)  /* RX ok */
#define INTR_TOK            BIT(2)  /* TX ok */
#define INTR_LINK_CHG       BIT(5)  /* Link change */
#define INTR_RDU            BIT(4)  /* RX descriptor unavail */

/* TxConfig: chip version bits for RTL8168GU (VER_42).
 * The r8169 driver extracts XID = (TxConfig >> 20) & 0xfcf, then matches
 * against { mask=0x7cf, val=0x509 } → RTL_GIGA_MAC_VER_42, firmware
 * rtl_nic/rtl8168g-2.fw.  So we need (TxConfig >> 20) & 0xfcf == 0x509.
 */
#define TXCFG_EMPTY         BIT(11)
#define TXCONFIG_VER42      ((0x509u << 20) | TXCFG_EMPTY)

/* Descriptor format */
#define DESC_OWN            BIT(31)
#define DESC_EOR            BIT(30) /* End of ring */
#define DESC_FS             BIT(29) /* First segment */
#define DESC_LS             BIT(28) /* Last segment */
#define DESC_LEN_MASK       0x3fff

#define NUM_TX_DESC         256
#define NUM_RX_DESC         256
#define RX_BUF_SIZE         1536

/* Firmware detection: the r8169 driver's firmware loading performs many
 * PHY writes through PHYAR. Normal init does < 20 PHY writes; firmware
 * loading does 50+. We use a threshold to detect firmware presence.
 */
#define FW_PHY_WRITE_THRESHOLD  30

/* Descriptor layout (16 bytes, little-endian in guest memory) */
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

    /* MMIO */
    MemoryRegion mmio;
    uint8_t regs[0x100];

    /* NIC backend */
    NICState *nic;
    NICConf conf;

    /* Descriptor ring state */
    uint64_t tx_desc_addr;
    uint64_t rx_desc_addr;
    uint32_t tx_cur;
    uint32_t rx_cur;

    /* Interrupt */
    uint16_t intr_mask;
    uint16_t intr_status;

    /* PHY */
    uint16_t phy_regs[32];
    uint32_t phy_write_count;
    bool fw_loaded;

    /* Config */
    bool cfg_unlocked;
    uint8_t chip_cmd;
};

/* ── Forward declarations ────────────────────────────────────── */

static void rtl8168_update_irq(RTL8168State *s);
static void rtl8168_tx(RTL8168State *s);

/* ── Interrupt handling ──────────────────────────────────────── */

static void rtl8168_update_irq(RTL8168State *s)
{
    int level = !!(s->intr_status & s->intr_mask);
    pci_set_irq(&s->parent_obj, level);
}

static void rtl8168_set_intr(RTL8168State *s, uint16_t bits)
{
    s->intr_status |= bits;
    rtl8168_update_irq(s);
}

/* ── PHYstatus: link up only when firmware loaded ────────────── */

static uint8_t rtl8168_phystatus(RTL8168State *s)
{
    if (!s->fw_loaded) {
        return 0;  /* link down */
    }
    /* 1Gbps full duplex link up */
    return PHYSTATUS_LINK | PHYSTATUS_FULLDUP | PHYSTATUS_1000MF |
           PHYSTATUS_TXFLOW | PHYSTATUS_RXFLOW;
}

/* ── TX path ─────────────────────────────────────────────────── */

static void rtl8168_tx(RTL8168State *s)
{
    if (!(s->chip_cmd & CHIPCMD_TE) || !s->fw_loaded) {
        return;
    }
    if (!s->tx_desc_addr) {
        return;
    }

    for (int budget = NUM_TX_DESC; budget > 0; budget--) {
        RTL8168Desc desc;
        hwaddr daddr = s->tx_desc_addr + s->tx_cur * sizeof(desc);

        pci_dma_read(&s->parent_obj, daddr, &desc, sizeof(desc));
        desc.opts1 = le32_to_cpu(desc.opts1);
        desc.opts2 = le32_to_cpu(desc.opts2);
        desc.addr_lo = le32_to_cpu(desc.addr_lo);
        desc.addr_hi = le32_to_cpu(desc.addr_hi);

        if (!(desc.opts1 & DESC_OWN)) {
            break;
        }

        uint32_t len = desc.opts1 & DESC_LEN_MASK;
        if (len > 0 && len <= 9000) {
            uint8_t buf[9000];
            hwaddr baddr = ((uint64_t)desc.addr_hi << 32) | desc.addr_lo;

            pci_dma_read(&s->parent_obj, baddr, buf, len);
            qemu_send_packet(qemu_get_queue(s->nic), buf, len);
        }

        /* Clear OWN, keep EOR */
        desc.opts1 = cpu_to_le32(desc.opts1 & DESC_EOR);
        pci_dma_write(&s->parent_obj, daddr, &desc.opts1, 4);

        bool eor = !!(le32_to_cpu(desc.opts1) & DESC_EOR);
        s->tx_cur = eor ? 0 : (s->tx_cur + 1) % NUM_TX_DESC;
    }

    rtl8168_set_intr(s, INTR_TOK);
}

/* ── RX path ─────────────────────────────────────────────────── */

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

    /* Write packet data to guest buffer */
    hwaddr baddr = ((uint64_t)desc.addr_hi << 32) | desc.addr_lo;
    pci_dma_write(&s->parent_obj, baddr, buf, size);

    /* Update descriptor: clear OWN, set FS+LS, set length */
    uint32_t new_opts1 = (desc.opts1 & DESC_EOR) | DESC_FS | DESC_LS |
                         (size & DESC_LEN_MASK);
    new_opts1 = cpu_to_le32(new_opts1);
    pci_dma_write(&s->parent_obj, daddr, &new_opts1, 4);

    bool eor = !!(desc.opts1 & DESC_EOR);
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
        return 0x0000e70e;  /* typical RxConfig default */
    case REG_INTRMASK:
        return s->intr_mask;
    case REG_INTRSTATUS:
        return s->intr_status;
    case REG_PHYAR:
        /* Return last PHY read result; clear busy flag */
        return s->regs[REG_PHYAR + 3] & 0x7f ? 0 :
               ldl_le_p(&s->regs[REG_PHYAR]);
    case REG_PHYSTATUS:
        return rtl8168_phystatus(s);
    case REG_CFG9346:
        return s->cfg_unlocked ? CFG9346_UNLOCK : CFG9346_LOCK;
    case REG_CONFIG0 ... REG_CONFIG5:
        return s->regs[addr];
    case REG_CPLUSCMD:
        return 0x2060;  /* C+ mode enabled, VLAN de-tag */
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
        return ldl_le_p(&s->regs[REG_OCPDR]) & ~0x80000000u;  /* data, flag clear */
    case REG_OCPAR:
        return ldl_le_p(&s->regs[REG_OCPAR]) | 0x80000000u;  /* access done */
    case REG_GPHY_OCP: {
        /* Write cmd (bit31=1): polls wait_low → return flag clear.
         * Read cmd  (bit31=0): polls wait_high → return flag set + PHY data.
         * OCP PHY address: reg field is (val >> 15), OCP base 0xa400. */
        uint32_t v = ldl_le_p(&s->regs[REG_GPHY_OCP]);
        if (v & 0x80000000u) {
            return v & ~0x80000000u;  /* write done */
        }
        /* Read: extract OCP reg, return PHY data with flag set */
        uint32_t ocp_reg = (v >> 15) & 0xffff;
        uint16_t data = 0;
        if (ocp_reg >= 0xa400 && ocp_reg < 0xa400 + 64) {
            int phyreg = (ocp_reg - 0xa400) / 2;
            if (phyreg < 32) {
                data = s->phy_regs[phyreg];
            }
        }
        return 0x80000000u | data;
    }
    case REG_MCU:
        return MCU_READY_BITS;  /* FIFOs empty, link list ready */
    case REG_ERIAR:
        return ERIAR_FLAG;  /* ERI access always complete */
    case REG_ERIDR:
        return 0;
    case REG_CSIAR:
        return CSIAR_FLAG;  /* CSI access always complete */
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
    case REG_MAC0:
        if (s->cfg_unlocked) {
            stl_le_p(s->conf.macaddr.a, val);
        }
        break;
    case REG_MAC4:
        if (s->cfg_unlocked) {
            stw_le_p(s->conf.macaddr.a + 4, val);
        }
        break;
    case REG_MAR0:
        stl_le_p(&s->regs[REG_MAR0], val);
        break;
    case REG_MAR4:
        stl_le_p(&s->regs[REG_MAR4], val);
        break;
    case REG_CHIPCMD:
        if (val & CHIPCMD_RST) {
            /* Soft reset */
            s->chip_cmd = 0;
            s->intr_status = 0;
            s->intr_mask = 0;
            s->tx_cur = 0;
            s->rx_cur = 0;
            /* Do NOT reset fw_loaded — firmware persists across reset */
            rtl8168_update_irq(s);
        } else {
            s->chip_cmd = val & (CHIPCMD_TE | CHIPCMD_RE);
        }
        break;
    case REG_TXPOLL:
        if (val & (TXPOLL_HPQ | TXPOLL_NPQ)) {
            rtl8168_tx(s);
        }
        break;
    case REG_INTRMASK:
        s->intr_mask = val;
        rtl8168_update_irq(s);
        break;
    case REG_INTRSTATUS:
        /* Write 1 to clear */
        s->intr_status &= ~val;
        rtl8168_update_irq(s);
        break;
    case REG_TXCONFIG:
        /* Read-only version bits; store the rest */
        break;
    case REG_RXCONFIG:
        stl_le_p(&s->regs[REG_RXCONFIG], val);
        break;
    case REG_CFG9346:
        s->cfg_unlocked = ((val & 0xc0) == CFG9346_UNLOCK);
        break;
    case REG_CONFIG0 ... REG_CONFIG5:
        if (s->cfg_unlocked) {
            s->regs[addr] = val;
        }
        break;
    case REG_PHYAR: {
        /*
         * PHY access register. Bit 31 = write flag.
         * The r8169 firmware loading writes PHY registers through here.
         * We count PHY writes — above a threshold we consider firmware
         * loaded and bring the link up.
         */
        uint32_t v = (uint32_t)val;
        uint8_t reg = (v >> 16) & 0x1f;
        uint16_t data = v & 0xffff;

        if (v & PHYAR_FLAG) {
            /* PHY write */
            s->phy_regs[reg] = data;
            s->phy_write_count++;

            if (!s->fw_loaded &&
                s->phy_write_count >= FW_PHY_WRITE_THRESHOLD) {
                s->fw_loaded = true;
                rtl8168_set_intr(s, INTR_LINK_CHG);
            }
            /* Clear busy flag after write */
            stl_le_p(&s->regs[REG_PHYAR], v & ~PHYAR_FLAG);
        } else {
            /* PHY read — return register value, set data bits */
            uint32_t result = (v & 0xffff0000) | s->phy_regs[reg];
            stl_le_p(&s->regs[REG_PHYAR], result);
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
        s->tx_desc_addr = (s->tx_desc_addr & 0xffffffff00000000ULL) | val;
        s->tx_cur = 0;
        break;
    case REG_TXDESC_HI:
        s->tx_desc_addr = (s->tx_desc_addr & 0x00000000ffffffffULL) |
                          ((uint64_t)val << 32);
        break;
    case REG_RXDESC_LO:
        s->rx_desc_addr = (s->rx_desc_addr & 0xffffffff00000000ULL) | val;
        s->rx_cur = 0;
        break;
    case REG_RXDESC_HI:
        s->rx_desc_addr = (s->rx_desc_addr & 0x00000000ffffffffULL) |
                          ((uint64_t)val << 32);
        break;
    case REG_MAXTXPKTSIZE:
        s->regs[REG_MAXTXPKTSIZE] = val;
        break;
    case REG_OCPDR:
    case REG_OCPAR:
    case REG_ERIDR:
    case REG_ERIAR:
    case REG_CSIAR:
    case REG_CSIDR:
        /* Indirect access registers — store value, flag auto-clears on read */
        stl_le_p(&s->regs[addr], val);
        break;
    case REG_GPHY_OCP: {
        /* OCP PHY access. Write cmd (bit31=1) writes PHY reg + data.
         * Track PHY writes for firmware detection. */
        stl_le_p(&s->regs[REG_GPHY_OCP], val);
        if (val & 0x80000000u) {
            uint32_t ocp_reg = (val >> 15) & 0xffff;
            uint16_t data = val & 0xffff;
            if (ocp_reg >= 0xa400 && ocp_reg < 0xa400 + 64) {
                int phyreg = (ocp_reg - 0xa400) / 2;
                if (phyreg < 32) {
                    s->phy_regs[phyreg] = data;
                    /* Auto-clear BMCR reset bit (PHY reset completes
                     * instantly in emulation) */
                    if (phyreg == 0 && (data & 0x8000)) {
                        s->phy_regs[0] = data & ~0x8000;
                    }
                }
            }
            s->phy_write_count++;
            if (!s->fw_loaded &&
                s->phy_write_count >= FW_PHY_WRITE_THRESHOLD) {
                s->fw_loaded = true;
                rtl8168_set_intr(s, INTR_LINK_CHG);
            }
        }
        break;
    }
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
    .impl = {
        .min_access_size = 1,
        .max_access_size = 4,
    },
};

/* ── NIC callbacks ───────────────────────────────────────────── */

static void rtl8168_set_link_status(NetClientState *nc)
{
    /* Link status is controlled by firmware gate, not external state */
}

static NetClientInfo rtl8168_net_info = {
    .type = NET_CLIENT_DRIVER_NIC,
    .size = sizeof(NICState),
    .can_receive = rtl8168_can_receive,
    .receive = rtl8168_receive,
    .link_status_changed = rtl8168_set_link_status,
};

/* ── PCI device lifecycle ────────────────────────────────────── */

static void rtl8168_realize(PCIDevice *pci_dev, Error **errp)
{
    RTL8168State *s = RTL8168(pci_dev);

    pci_dev->config[PCI_INTERRUPT_PIN] = 1;  /* INTA# */

    memory_region_init_io(&s->mmio, OBJECT(s), &rtl8168_mmio_ops, s,
                          "rtl8168-mmio", 0x100);
    pci_register_bar(pci_dev, 2, PCI_BASE_ADDRESS_SPACE_MEMORY, &s->mmio);

    s->nic = qemu_new_nic(&rtl8168_net_info, &s->conf,
                           object_get_typename(OBJECT(s)),
                           pci_dev->qdev.id,
                           &pci_dev->qdev.mem_reentrancy_guard, s);
    qemu_format_nic_info_str(qemu_get_queue(s->nic), s->conf.macaddr.a);

    /* Initial state: link down (no firmware), TX/RX disabled */
    s->fw_loaded = false;
    s->phy_write_count = 0;
    s->chip_cmd = 0;
    s->tx_cur = 0;
    s->rx_cur = 0;

    /* PHY registers — Realtek internal PHY for RTL8168G */
    s->phy_regs[0]  = 0x1140;  /* MII_BMCR: autoneg enable, 1Gbps */
    s->phy_regs[1]  = 0x796d;  /* MII_BMSR: link up, autoneg capable/complete */
    s->phy_regs[2]  = 0x001c;  /* MII_PHYSID1: Realtek OUI */
    s->phy_regs[3]  = 0xc912;  /* MII_PHYSID2: RTL8168G */
    s->phy_regs[4]  = 0x01e1;  /* MII_ADVERTISE: 100/10 FD/HD, 802.3 */
    s->phy_regs[5]  = 0xc5e1;  /* MII_LPA: partner advert */
    s->phy_regs[6]  = 0x000f;  /* MII_EXPANSION */
    s->phy_regs[9]  = 0x0200;  /* MII_CTRL1000: advertise 1000baseT FD */
    s->phy_regs[10] = 0x3c00;  /* MII_STAT1000: partner 1000baseT FD */
    s->phy_regs[15] = 0x3000;  /* MII_ESTATUS: 1000baseT FD/HD capable */
}

static void rtl8168_exit(PCIDevice *pci_dev)
{
    RTL8168State *s = RTL8168(pci_dev);
    qemu_del_nic(s->nic);
}

static void rtl8168_reset(DeviceState *dev)
{
    RTL8168State *s = RTL8168(dev);

    memset(s->regs, 0, sizeof(s->regs));
    s->chip_cmd = 0;
    s->intr_mask = 0;
    s->intr_status = 0;
    s->tx_desc_addr = 0;
    s->rx_desc_addr = 0;
    s->tx_cur = 0;
    s->rx_cur = 0;
    s->fw_loaded = false;
    s->phy_write_count = 0;
    s->cfg_unlocked = false;
}

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
