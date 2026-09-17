package workspace

import (
	"encoding/json"
	"fmt"
	"io"
)

// WriteText prints the proposal's scene / status / detail columns.
func WriteText(w io.Writer, res *Result) error {
	if res == nil {
		return nil
	}
	if len(res.Warnings) > 0 {
		if _, err := fmt.Fprintln(w, "Warnings:"); err != nil {
			return err
		}
		for _, warn := range res.Warnings {
			if _, err := fmt.Fprintf(w, "  %s: %s\n", warn.Path, warn.Error); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
	}
	if len(res.Errors) > 0 {
		if _, err := fmt.Fprintln(w, "Errors:"); err != nil {
			return err
		}
		ew := 0
		for _, s := range res.Errors {
			if n := len(displayName(s)); n > ew {
				ew = n
			}
		}
		if ew < 15 {
			ew = 15
		}
		for _, s := range res.Errors {
			if _, err := fmt.Fprintf(w, "  %-*s %-22s %s\n", ew, displayName(s), s.Status, s.Detail); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
	}
	width := 0
	for _, s := range res.Scenes {
		if n := len(displayName(s)); n > width {
			width = n
		}
	}
	if width < 15 {
		width = 15
	}
	for _, s := range res.Scenes {
		if _, err := fmt.Fprintf(w, "%-*s %-22s %s\n", width, displayName(s), s.Status, s.Detail); err != nil {
			return err
		}
	}
	return nil
}

func displayName(s SceneStatus) string {
	rel := s.ProjectRel
	if s.Scene == "" {
		if rel == "" || rel == "." {
			return "."
		}
		return rel
	}
	if rel == "" || rel == "." {
		return "./" + s.Scene
	}
	return rel + "/" + s.Scene
}

// WriteJSON encodes the report, including generation or legacy paths.
func WriteJSON(w io.Writer, res *Result) error {
	if res == nil {
		res = &Result{Scenes: []SceneStatus{}, Errors: []SceneStatus{}, States: []StateLabel{}, Warnings: []Warning{}}
	}
	if res.Scenes == nil {
		res.Scenes = []SceneStatus{}
	}
	if res.Errors == nil {
		res.Errors = []SceneStatus{}
	}
	if res.States == nil {
		res.States = []StateLabel{}
	}
	if res.Warnings == nil {
		res.Warnings = []Warning{}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(res)
}
