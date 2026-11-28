package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/DemiCloud/doszer/internal/config"
	"github.com/DemiCloud/doszer/internal/install"
	"github.com/DemiCloud/doszer/internal/manifest"
	"github.com/DemiCloud/doszer/internal/output"
	"github.com/DemiCloud/doszer/internal/padmap"
	"github.com/DemiCloud/doszer/internal/repack"
	"github.com/DemiCloud/doszer/internal/runlog"
)

// runPad implements `doszer pad FILE.dosz`: the gamepad mapping an
// archive's manifest gives, or the one DOSBox Pure's Controller Mapper
// saved for the game, written into the manifest with --apply.
func runPad(args []string) error {
	fs := newFlags("pad")
	fromSave := fs.Bool("from-save", false, "")
	apply := fs.Bool("apply", false, "")
	saveDir := fs.String("save-dir", "", "")
	const help = `doszer pad - show a game's gamepad mapping, or take the one made in DOSBox Pure

Usage: doszer pad [options] FILE.dosz

Options:
      --apply             --from-save: write the mapping into the archive's manifest
      --from-save         the mapping DOSBox Pure's Controller Mapper saved for the game
` + flagSaveDir + flagNoLog + flagFormatData + flagHelp
	if ok, err := parse(fs, args, help, 1, 1); !ok {
		return err
	}
	if *apply && !*fromSave {
		return errors.New("--apply takes the mapping --from-save")
	}
	if !*apply {
		runlog.Discard() // only shows: nothing to record
	}
	archive := fs.Arg(0)
	m, err := archiveManifest(archive)
	if err != nil {
		return err
	}
	pads := m.Pads()
	if *fromSave {
		cfg, err := config.Load("")
		if err != nil {
			return err
		}
		dir, err := install.GameSaveDir(archive, stringOr(*saveDir, cfg.Paths.Saves))
		if err != nil {
			return err
		}
		saved, where, err := savedPadmap(archive, dir)
		if err != nil {
			return err
		}
		// Labels are DOS.YML's: the manifest's own, else the archive's.
		from := pads
		if data := archiveFile(archive, "DOS.YML"); data != nil {
			from = append(from, padmap.ParseDOSYML(data))
		}
		pads = padmap.Label(saved, from...)
		fmt.Fprintf(stderr, "From %s\n", where)
	}
	for i, p := range pads {
		for _, err := range p.Validate() {
			return fmt.Errorf("port %d: %w", i+1, err)
		}
	}
	in := manifest.Input{}
	for _, p := range pads {
		in.Pad = append(in.Pad, manifest.PadFrom(p))
	}

	if *apply {
		m.Input = in
		return applyManifest(archive, m)
	}
	switch format {
	case output.Fancy, output.Plain:
		if len(in.Pad) == 0 {
			fmt.Fprintln(stdout, "No gamepad mapping")
			return nil
		}
		data, err := toml.Marshal(struct {
			Input manifest.Input `toml:"input"`
		}{in})
		if err != nil {
			return err
		}
		_, err = stdout.Write(data)
		return err
	}
	return output.Encode(stdout, format, struct {
		Pads []manifest.Pad `json:"pads" yaml:"pads" toml:"pads"`
	}{in.Pad})
}

// archiveManifest reads an archive's embedded manifest.
func archiveManifest(archive string) (*manifest.Manifest, error) {
	data := archiveFile(archive, manifest.FileName)
	if data == nil {
		return nil, fmt.Errorf("%s: no manifest", archive)
	}
	stem := strings.TrimSuffix(filepath.Base(archive), filepath.Ext(archive))
	return manifest.Parse(data, stem)
}

// savedPadmap reads the mapping DOSBox Pure's mapper saved for archive.
func savedPadmap(archive, saveDir string) ([]padmap.Pad, string, error) {
	data, where, err := install.SavedPadmap(archive, saveDir)
	if err != nil {
		return nil, "", err
	}
	pads, err := padmap.DecodePADMAP(data)
	return pads, where, err
}

// applyManifest rebuilds archive in place with m as its manifest.
func applyManifest(archive string, m *manifest.Manifest) error {
	data, err := toml.Marshal(m)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp("", "doszer-manifest-*.toml")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	f.Close()
	prov, err := provenance()
	if err != nil {
		return err
	}
	res, err := repack.Run(repack.Options{InputPath: archive, OutputPath: archive, ManifestPath: f.Name(), Provenance: prov})
	endSteps(err)
	if res != nil {
		recordBuild(res)
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Wrote the mapping into %s (%s)\n", archive, plural(len(m.Input.Pad), "port"))
	return nil
}
