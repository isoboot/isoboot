# BootSource State Machine

This document describes the state machine that governs BootSource resource lifecycle.

## Phases

| Phase | Description |
|-------|-------------|
| `Pending` | Initial state when BootSource is created |
| `Downloading` | Fetching kernel, initrd, ISO, and/or firmware from URLs |
| `Verifying` | Validating downloaded files against checksums |
| `Extracting` | Extracting kernel/initrd from ISO (ISO mode only) |
| `Building` | Combining initrd with firmware (when firmware specified) |
| `Ready` | All resources available and verified |
| `Corrupted` | Checksum verification failed |
| `Failed` | Unrecoverable error occurred |

## State Transitions

> **NOTE:** State transition logic is NOT YET IMPLEMENTED. The Reconcile function
> is currently a stub. Tests for these transitions do not exist yet.

| From | To | Condition | Tested |
|------|-----|-----------|--------|
| (new) | Pending | BootSource created | :x: |
| Pending | Downloading | Reconciler starts processing | :x: |
| Downloading | Verifying | All downloads completed successfully | :x: |
| Downloading | Failed | Network error, HTTP error, or timeout | :x: |
| Verifying | Extracting | Hash verified, ISO mode, need to extract | :x: |
| Verifying | Building | Hash verified, firmware specified, need to combine | :x: |
| Verifying | Ready | Hash verified, no extraction or building needed | :x: |
| Verifying | Corrupted | Hash mismatch detected | :x: |
| Extracting | Building | Extraction complete, firmware specified | :x: |
| Extracting | Ready | Extraction complete, no firmware | :x: |
| Extracting | Failed | Extraction error (file not found, corrupt ISO) | :x: |
| Building | Ready | Initrd + firmware combined successfully | :x: |
| Building | Failed | Build error (cpio/gzip failure) | :x: |
| Ready | Verifying | Re-verification triggered (e.g., file watcher) | :x: |
| Corrupted | Downloading | Re-download triggered (manual or automatic) | :x: |

## Terminal States

- **Ready**: Success state, resources available for PXE/iPXE serving
- **Failed**: Unrecoverable error, requires spec change or manual intervention
- **Corrupted**: Recoverable error, can retry download

## Mode-Specific Flows

### Kernel + Initrd mode (no firmware)

```
Pending → Downloading → Verifying → Ready
```

### Kernel + Initrd mode (with firmware)

```
Pending → Downloading → Verifying → Building → Ready
```

### ISO mode (no firmware)

```
Pending → Downloading → Verifying → Extracting → Ready
```

### ISO mode (with firmware)

```
Pending → Downloading → Verifying → Extracting → Building → Ready
```

## State Diagram

```
                              ┌─────────┐
                              │ Pending │
                              └────┬────┘
                                   │
                                   ▼
                            ┌─────────────┐
                            │ Downloading │
                            └──────┬──────┘
                                   │
                       ┌───────────┴───────────┐
                       ▼                       ▼
                  ┌────────┐            ┌───────────┐
                  │ Failed │            │ Verifying │
                  └────────┘            └─────┬─────┘
                                              │
                      ┌───────────────────────┼───────────────────────┐
                      ▼                       ▼                       ▼
                 ┌───────────┐         ┌────────────┐          ┌─────────┐
                 │ Corrupted │         │ Extracting │          │ Building│
                 └───────────┘         └─────┬──────┘          └────┬────┘
                      │                      │                      │
                      │              ┌───────┴───────┐              │
                      │              ▼               ▼              │
                      │         ┌────────┐    ┌──────────┐          │
                      │         │ Failed │    │ Building │          │
                      │         └────────┘    └────┬─────┘          │
                      │                            │                │
                      │                    ┌───────┴───────┐        │
                      │                    ▼               ▼        │
                      │               ┌────────┐      ┌─────────┐   │
                      │               │ Failed │      │  Ready  │◄──┘
                      │               └────────┘      └────┬────┘
                      │                                    │
                      └────────────────────────────────────┘
                              (re-verification triggered)
```
