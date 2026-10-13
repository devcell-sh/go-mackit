//go:build darwin && arm64

package vz

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Code-Hex/vz/v3"
)

type platformData struct {
	ECID          []byte `json:"ecid"`
	HardwareModel []byte `json:"hardware_model"`
}

func savePlatformData(outDir string, ecid *vz.MacMachineIdentifier, hwModel *vz.MacHardwareModel) error {
	data := platformData{
		ECID:          ecid.DataRepresentation(),
		HardwareModel: hwModel.DataRepresentation(),
	}
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(outDir, "platform.json"), b, 0o644)
}

func loadPlatformData(outDir string) (*vz.MacMachineIdentifier, *vz.MacHardwareModel, error) {
	b, err := os.ReadFile(filepath.Join(outDir, "platform.json"))
	if err != nil {
		return nil, nil, fmt.Errorf("reading platform data: %w", err)
	}
	var data platformData
	if err := json.Unmarshal(b, &data); err != nil {
		return nil, nil, err
	}
	ecid, err := vz.NewMacMachineIdentifierWithData(data.ECID)
	if err != nil {
		return nil, nil, fmt.Errorf("restoring ECID: %w", err)
	}
	hwModel, err := vz.NewMacHardwareModelWithData(data.HardwareModel)
	if err != nil {
		return nil, nil, fmt.Errorf("restoring hardware model: %w", err)
	}
	return ecid, hwModel, nil
}

