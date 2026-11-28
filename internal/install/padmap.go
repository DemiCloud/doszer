package install

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// SavedPadmap returns the PADMAP.DBP DOSBox Pure's Controller Mapper
// saved for archive, and where it is: in the game's C: save,
// <name>.pure.zip in saveDir (the mapper writes it to C:,
// dosbox_pure_pad.h, DBP_PadMapping::Save, which the save keeps).
func SavedPadmap(archive, saveDir string) ([]byte, string, error) {
	stem := strings.TrimSuffix(filepath.Base(archive), filepath.Ext(archive))
	save := filepath.Join(saveDir, stem+".pure.zip")
	zr, err := zip.OpenReader(save)
	if errors.Is(err, os.ErrNotExist) {
		return nil, "", fmt.Errorf("%s: no save (play the game and map its controls in DOSBox Pure's Controller Mapper first)", save)
	}
	if err != nil {
		return nil, "", err
	}
	defer zr.Close()
	for _, f := range zr.File {
		if !strings.EqualFold(f.Name, "PADMAP.DBP") {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, "", err
		}
		data, err := io.ReadAll(io.LimitReader(rc, 1<<16))
		rc.Close()
		return data, save + ": PADMAP.DBP", err
	}
	return nil, "", fmt.Errorf("%s: no PADMAP.DBP (the mapping is the game's default: change a binding in DOSBox Pure's Controller Mapper first)", save)
}
