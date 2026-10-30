package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/CornHead764/agentifi/backend/internal/categoryeval"
	"github.com/CornHead764/agentifi/backend/internal/config"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
	"github.com/google/uuid"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func evalCommand(ctx context.Context, cfg *config.Config, args []string, out, summary io.Writer) error {
	if len(args) == 0 || args[0] != "categorize" {
		return fmt.Errorf("usage: agentifi eval categorize --household <id> [--holdout 0.2 | --cutoff YYYY-MM-DD] [--with-model] [--out report.json]")
	}
	fs := flag.NewFlagSet("eval categorize", flag.ContinueOnError)
	household := fs.String("household", "", "household id")
	holdout := fs.Float64("holdout", 0.2, "fraction of labelled postings held out (0 < fraction < 1)")
	cutoff := fs.String("cutoff", "", "first held-out effective date (YYYY-MM-DD); overrides --holdout")
	model := fs.Bool("with-model", false, "allow sending held-out rows and training context to the configured model")
	path := fs.String("out", "", "write JSON to this file instead of stdout")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("eval categorize: unexpected positional arguments")
	}
	id, err := uuid.Parse(*household)
	if err != nil {
		return fmt.Errorf("eval categorize: --household must be a UUID")
	}
	opts := categoryeval.Options{Household: store.SpaceID(id), Holdout: *holdout, WithModel: *model}
	if *cutoff != "" {
		opts.Cutoff, err = domain.ParseDate(*cutoff)
		if err != nil {
			return err
		}
	}
	if *path != "" {
		destination, err := filepath.Abs(*path)
		if err != nil {
			return err
		}
		data, err := filepath.Abs(cfg.DataDir)
		if err != nil {
			return err
		}
		if resolved, err := filepath.EvalSymlinks(data); err == nil {
			data = resolved
		}
		parent, err := filepath.EvalSymlinks(filepath.Dir(destination))
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(data, filepath.Join(parent, filepath.Base(destination)))
		if err != nil {
			return err
		}
		if relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return fmt.Errorf("eval categorize: --out must be outside DATA_DIR")
		}
	}
	lock, err := lockDataDir(cfg)
	if err != nil {
		return err
	}
	defer lock.Close()
	report, err := categoryeval.Run(ctx, cfg, opts)
	if err != nil {
		return err
	}
	encoded, err := report.JSON()
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	if *path != "" {
		file, createErr := os.CreateTemp(filepath.Dir(*path), ".categorize-*.json")
		if createErr != nil {
			return createErr
		}
		defer os.Remove(file.Name())
		_, err = file.Write(encoded)
		closeErr := file.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		err = os.Rename(file.Name(), *path)
	} else {
		_, err = out.Write(encoded)
	}
	if err != nil {
		return err
	}
	m := report.Score.Overall
	fmt.Fprintf(summary, "Categorization: %d training labels, %d held-out postings; cutoff %s.\nCoverage %s (%d/%d); accuracy among suggestions %s (%d/%d).\n", report.TrainingRows, report.HeldOutRows, report.Cutoff, evalPercent(m.Coverage), m.Suggested, m.Rows, evalPercent(m.Accuracy), m.Correct, m.Suggested)
	for _, c := range report.Score.Confusions {
		fmt.Fprintf(summary, "%s → %s: %d\n", c.TrueCategory, c.SuggestedCategory, c.Count)
	}
	return nil
}

func evalPercent(value *float64) string {
	if value == nil {
		return "n/a"
	}
	return fmt.Sprintf("%.1f%%", 100**value)
}
