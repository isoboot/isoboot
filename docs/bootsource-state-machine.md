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

> **Implementation Status:**
> - **Direct mode (kernel+initrd)**: Implemented and tested
> - **ISO mode**: Implemented and tested
> - **Building phase (firmware combining)**: Implemented and tested
>
> The current implementation performs download and verification synchronously within
> a single reconcile, transitioning directly to the final phase (Ready/Failed/Corrupted)
> rather than showing intermediate Downloading/Verifying phases.

| From | To | Condition | Tested |
|------|-----|-----------|--------|
| (new) | Pending | BootSource created, ISO mode | :white_check_mark: |
| (new) | Ready | Direct mode, download + verify succeeds | :white_check_mark: |
| (new) | Failed | Direct mode, network error or hash fetch fails | :white_check_mark: |
| (new) | Corrupted | Direct mode, hash verification fails | :white_check_mark: |
| Pending | Downloading | Reconciler starts processing | :x: (async) |
| Downloading | Verifying | All downloads completed successfully | :x: (async) |
| Downloading | Failed | Network error, HTTP error, or timeout | :x: (async) |
| Verifying | Extracting | Hash verified, ISO mode, need to extract | :white_check_mark: |
| Verifying | Building | Hash verified, firmware specified, need to combine | :white_check_mark: |
| Verifying | Ready | Hash verified, no extraction or building needed | :x: (async) |
| Verifying | Corrupted | Hash mismatch detected | :x: (async) |
| Extracting | Building | Extraction complete, firmware specified | :white_check_mark: |
| Extracting | Ready | Extraction complete, no firmware | :white_check_mark: |
| Extracting | Failed | Extraction error (file not found, corrupt ISO) | :white_check_mark: |
| Building | Ready | Initrd + firmware combined successfully | :white_check_mark: |
| Building | Failed | Build error (I/O failure during concatenation) | :white_check_mark: |
| Ready | Verifying | Re-verification triggered (e.g., file watcher) | :x: |
| Corrupted | Downloading | Re-download triggered (manual or automatic) | :x: |

## Terminal States

- **Ready**: Success state, resources available for PXE/iPXE serving
- **Failed**: Unrecoverable error, requires spec change or manual intervention
- **Corrupted**: Recoverable error, can retry download

## Requeue Behavior

| Phase | Requeue Interval |
|-------|------------------|
| Ready | No requeue |
| Downloading, Verifying, Extracting, Building, Pending | 30 seconds |
| Failed, Corrupted | 5 minutes |

## Deletion / Cleanup

BootSource resources use a finalizer (`isoboot.github.io/cleanup`) to ensure downloaded
files are cleaned up when the resource is deleted:

1. User deletes BootSource
2. Kubernetes sets `DeletionTimestamp`
3. Reconciler detects deletion, removes files from `<baseDir>/<namespace>/<name>/`
4. Reconciler removes finalizer
5. Kubernetes garbage collects the resource

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
