# GPU health checks

The device plugin uses NVML XID events to mark GPUs unhealthy. XID 154 reports a
change in the GPU recovery action; the event number alone does not say whether
recovery is needed. For this event, the plugin queries
`GetFieldValues(FI_DEV_GET_GPU_RECOVERY_ACTION)` on the affected GPU.

| NVML recovery action | Device plugin behavior |
| --- | --- |
| `NONE` | Ignore the event; do not mark the device unhealthy. |
| `GPU_RESET`, `NODE_REBOOT`, `DRAIN_P2P`, `DRAIN_AND_RESET`, `RECOVER_IMEX_DOMAIN`, `BUS_RESET`, `SYSTEM_REBOOT` | Log the requested recovery action and mark the GPU unhealthy. |
| Unknown action, failed query, failed field status, or unexpected value type | Log the failure and mark the GPU unhealthy conservatively. |

GPU recovery applies to every advertised device on the affected physical GPU,
including all MIG instances and replicas. Other GPUs remain unaffected. Other
XIDs retain their existing health-check behavior and MIG instance filtering.

The existing `DP_DISABLE_HEALTHCHECKS` and `DP_ENABLE_HEALTHCHECKS` controls still
apply before processing XID 154. Explicitly ignoring XID 154 also skips its
recovery-action query.

Marking a GPU unhealthy prevents new Kubernetes device allocations. The plugin
does not evict running workloads or perform resets, drains, or reboots; a recovery
controller or operator must coordinate those actions. A subsequent `NONE` event
does not restore a previously unhealthy device to healthy, consistent with the
plugin's existing health lifecycle.

The regression tests in `internal/rm/health_recovery_test.go` exercise the health
loop for all supported recovery actions, invalid responses, XID overrides, and
MIG scope. Run them with:

```sh
go test ./internal/rm -run TestCheckHealthGPURecoveryAction -count=1
```
