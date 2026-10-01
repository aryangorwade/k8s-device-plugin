/*
 * Copyright (c) 2026, NVIDIA CORPORATION.  All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY Type, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package rm

import (
	"encoding/binary"
	"testing"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"
	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"
)

func TestCheckHealthGPURecoveryAction(t *testing.T) {
	testCases := []struct {
		name          string
		action        uint32
		queryRet      nvml.Return
		fieldRet      uint32
		valueType     uint32
		xid           uint64
		disabled      string
		enabled       string
		unhealthy     bool
		expectedCalls int
	}{
		{name: "no recovery needed", expectedCalls: 1},
		{name: "GPU reset", action: uint32(nvml.GPU_RECOVERY_ACTION_GPU_RESET), unhealthy: true, expectedCalls: 1},
		{name: "node reboot", action: uint32(nvml.GPU_RECOVERY_ACTION_NODE_REBOOT), unhealthy: true, expectedCalls: 1},
		{name: "drain P2P", action: uint32(nvml.GPU_RECOVERY_ACTION_DRAIN_P2P), unhealthy: true, expectedCalls: 1},
		{name: "drain and reset", action: uint32(nvml.GPU_RECOVERY_ACTION_DRAIN_AND_RESET), unhealthy: true, expectedCalls: 1},
		{name: "recover IMEX domain", action: uint32(nvml.GPU_RECOVERY_ACTION_RECOVER_IMEX_DOMAIN), unhealthy: true, expectedCalls: 1},
		{name: "bus reset", action: uint32(nvml.GPU_RECOVERY_ACTION_BUS_RESET), unhealthy: true, expectedCalls: 1},
		{name: "system reboot", action: uint32(nvml.GPU_RECOVERY_ACTION_SYSTEM_REBOOT), unhealthy: true, expectedCalls: 1},
		{name: "unknown action", action: 0xffffffff, unhealthy: true, expectedCalls: 1},
		{name: "query failure", queryRet: nvml.ERROR_UNKNOWN, unhealthy: true, expectedCalls: 1},
		{name: "query unsupported", queryRet: nvml.ERROR_NOT_SUPPORTED, unhealthy: true, expectedCalls: 1},
		{name: "field unsupported", fieldRet: uint32(nvml.ERROR_NOT_SUPPORTED), unhealthy: true, expectedCalls: 1},
		{name: "field failure", fieldRet: uint32(nvml.ERROR_GPU_IS_LOST), unhealthy: true, expectedCalls: 1},
		{name: "unexpected value type", valueType: uint32(nvml.VALUE_TYPE_UNSIGNED_LONG_LONG), unhealthy: true, expectedCalls: 1},
		{name: "explicitly disabled", disabled: "154", action: uint32(nvml.GPU_RECOVERY_ACTION_GPU_RESET)},
		{name: "enabled overrides disabled", disabled: "154", enabled: "154", expectedCalls: 1},
		{name: "other critical XID", xid: 48, unhealthy: true},
		{name: "application XID", xid: 13},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(envDisableHealthChecks, tc.disabled)
			t.Setenv(envEnableHealthChecks, tc.enabled)
			gpu := &recoveryTestDevice{uuid: "GPU-0"}
			otherGPU := &recoveryTestDevice{uuid: "GPU-1"}
			calls := 0
			gpu.getFieldValues = func(values []nvml.FieldValue) nvml.Return {
				calls++
				require.Len(t, values, 1)
				require.EqualValues(t, nvml.FI_DEV_GET_GPU_RECOVERY_ACTION, values[0].FieldId)
				require.Zero(t, values[0].ScopeId)
				values[0].NvmlReturn = tc.fieldRet
				values[0].ValueType = uint32(nvml.VALUE_TYPE_UNSIGNED_INT)
				if tc.valueType != 0 {
					values[0].ValueType = tc.valueType
				}
				// The unused part of NVML's union need not be zero for uiVal.
				values[0].Value = [8]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}
				binary.NativeEndian.PutUint32(values[0].Value[:4], tc.action)
				return tc.queryRet
			}
			xid := tc.xid
			if xid == 0 {
				xid = 154
			}
			devices := Devices{
				gpu.uuid:      {Device: pluginapi.Device{ID: gpu.uuid}, Index: "0"},
				otherGPU.uuid: {Device: pluginapi.Device{ID: otherGPU.uuid}, Index: "1"},
			}
			unhealthy := runRecoveryHealthCheck(t, devices, map[string]*recoveryTestDevice{
				gpu.uuid: gpu, otherGPU.uuid: otherGPU,
			}, nvml.EventData{
				Device: gpu, EventType: nvml.EventTypeXidCriticalError, EventData: xid,
				GpuInstanceId: nvml.GPU_INSTANCE_ID_ANY, ComputeInstanceId: nvml.COMPUTE_INSTANCE_ID_ANY,
			})
			if tc.unhealthy {
				require.Equal(t, []string{gpu.uuid}, unhealthy)
			} else {
				require.Empty(t, unhealthy)
			}
			require.Equal(t, tc.expectedCalls, calls)
		})
	}
}

func TestCheckHealthGPURecoveryActionMIG(t *testing.T) {
	for _, tc := range []struct {
		name     string
		xid      uint64
		action   uint32
		fieldRet uint32
		expected []string
	}{
		{name: "no recovery needed", xid: 154},
		{name: "GPU recovery affects all instances", xid: 154, action: uint32(nvml.GPU_RECOVERY_ACTION_GPU_RESET), expected: []string{"MIG-0", "MIG-1"}},
		{name: "unknown recovery affects all instances", xid: 154, fieldRet: uint32(nvml.ERROR_NOT_SUPPORTED), expected: []string{"MIG-0", "MIG-1"}},
		{name: "other XID remains instance scoped", xid: 48, expected: []string{"MIG-0"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(envDisableHealthChecks, "")
			t.Setenv(envEnableHealthChecks, "")
			gpu := &recoveryTestDevice{uuid: "GPU-0"}
			gpu.getFieldValues = func(values []nvml.FieldValue) nvml.Return {
				values[0].ValueType = uint32(nvml.VALUE_TYPE_UNSIGNED_INT)
				values[0].NvmlReturn = tc.fieldRet
				binary.NativeEndian.PutUint32(values[0].Value[:4], tc.action)
				return nvml.SUCCESS
			}
			handles := map[string]*recoveryTestDevice{
				gpu.uuid: gpu,
				"MIG-0":  {uuid: "MIG-0", parent: gpu, gi: 1, ci: 0},
				"MIG-1":  {uuid: "MIG-1", parent: gpu, gi: 2, ci: 0},
				"GPU-1":  {uuid: "GPU-1"},
			}
			devices := Devices{
				"MIG-0": {Device: pluginapi.Device{ID: "MIG-0"}, Index: "0:0"},
				"MIG-1": {Device: pluginapi.Device{ID: "MIG-1"}, Index: "0:1"},
				"GPU-1": {Device: pluginapi.Device{ID: "GPU-1"}, Index: "1"},
			}
			unhealthy := runRecoveryHealthCheck(t, devices, handles, nvml.EventData{
				Device: gpu, EventType: nvml.EventTypeXidCriticalError, EventData: tc.xid,
				GpuInstanceId: 1, ComputeInstanceId: 0,
			})
			require.ElementsMatch(t, tc.expected, unhealthy)
		})
	}
}

func runRecoveryHealthCheck(t *testing.T, devices Devices, handles map[string]*recoveryTestDevice, event nvml.EventData) []string {
	t.Helper()
	stop := make(chan any)
	events := &recoveryTestEventSet{event: event, stop: stop}
	r := &nvmlResourceManager{nvml: &recoveryTestNVML{handles: handles, events: events}}
	unhealthy := make(chan *Device, len(devices))
	require.NoError(t, r.checkHealth(stop, devices, unhealthy))
	close(unhealthy)
	var ids []string
	for d := range unhealthy {
		ids = append(ids, d.ID)
	}
	return ids
}

type recoveryTestNVML struct {
	nvml.Interface
	handles map[string]*recoveryTestDevice
	events  nvml.EventSet
}

func (n *recoveryTestNVML) Init() nvml.Return     { return nvml.SUCCESS }
func (n *recoveryTestNVML) Shutdown() nvml.Return { return nvml.SUCCESS }
func (n *recoveryTestNVML) EventSetCreate() (nvml.EventSet, nvml.Return) {
	return n.events, nvml.SUCCESS
}
func (n *recoveryTestNVML) DeviceGetHandleByUUID(uuid string) (nvml.Device, nvml.Return) {
	return n.handles[uuid], nvml.SUCCESS
}

type recoveryTestDevice struct {
	nvml.Device
	uuid           string
	parent         nvml.Device
	gi, ci         int
	getFieldValues func([]nvml.FieldValue) nvml.Return
}

func (d *recoveryTestDevice) GetUUID() (string, nvml.Return) { return d.uuid, nvml.SUCCESS }
func (d *recoveryTestDevice) GetSupportedEventTypes() (uint64, nvml.Return) {
	return nvml.EventTypeXidCriticalError, nvml.SUCCESS
}
func (d *recoveryTestDevice) RegisterEvents(uint64, nvml.EventSet) nvml.Return {
	return nvml.SUCCESS
}
func (d *recoveryTestDevice) GetFieldValues(values []nvml.FieldValue) nvml.Return {
	return d.getFieldValues(values)
}
func (d *recoveryTestDevice) GetDeviceHandleFromMigDeviceHandle() (nvml.Device, nvml.Return) {
	return d.parent, nvml.SUCCESS
}
func (d *recoveryTestDevice) GetGpuInstanceId() (int, nvml.Return)     { return d.gi, nvml.SUCCESS }
func (d *recoveryTestDevice) GetComputeInstanceId() (int, nvml.Return) { return d.ci, nvml.SUCCESS }

type recoveryTestEventSet struct {
	nvml.EventSet
	event     nvml.EventData
	stop      chan any
	delivered bool
}

func (s *recoveryTestEventSet) Free() nvml.Return { return nvml.SUCCESS }
func (s *recoveryTestEventSet) Wait(uint32) (nvml.EventData, nvml.Return) {
	if s.delivered {
		close(s.stop)
		return nvml.EventData{}, nvml.ERROR_TIMEOUT
	}
	s.delivered = true
	return s.event, nvml.SUCCESS
}
