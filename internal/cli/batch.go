package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/smkod/tts-cli/internal/core"
	"github.com/spf13/cobra"
)

type batchItem struct {
	Text     string
	Settings map[string]string
}

type itemResult struct {
	ID      string           `json:"id"`
	Success bool             `json:"success"`
	Data    *core.Generation `json:"data,omitempty"`
	Error   *core.Problem    `json:"error,omitempty"`
}
type manifest struct {
	Items     []itemResult `json:"items"`
	Total     int          `json:"total"`
	Succeeded int          `json:"succeeded"`
	Failed    int          `json:"failed"`
	DryRun    bool         `json:"dry_run,omitempty"`
}

func object(b []byte) (map[string]json.RawMessage, bool) {
	fields := map[string]json.RawMessage{}
	d := json.NewDecoder(bytes.NewReader(b))
	tok, err := d.Token()
	if err != nil || tok != json.Delim('{') {
		return fields, false
	}
	ok := true
	for d.More() {
		tok, err := d.Token()
		key, isString := tok.(string)
		var value json.RawMessage
		if err != nil || !isString || d.Decode(&value) != nil {
			return fields, false
		}
		if _, duplicate := fields[key]; duplicate {
			ok = false
		} else {
			fields[key] = value
		}
	}
	tok, err = d.Token()
	return fields, ok && err == nil && tok == json.Delim('}') && errors.Is(d.Decode(new(any)), io.EOF)
}

func jsonText(raw json.RawMessage) (string, bool) {
	var text string
	ok := len(raw) > 0 && raw[0] == '"' && json.Unmarshal(raw, &text) == nil
	return text, ok
}

func decodeItem(raw json.RawMessage) (string, batchItem, bool) {
	fields, valid := object(raw)
	id, validID := jsonText(fields["id"])
	text, validText := jsonText(fields["text"])
	item := batchItem{Text: text, Settings: map[string]string{}}
	valid = valid && validID && strings.TrimSpace(id) != "" && validText
	for k, value := range fields {
		switch k {
		case "id", "text":
		case "language", "voice":
			v, ok := jsonText(value)
			item.Settings[k] = v
			valid = valid && ok
		default:
			valid = false
		}
	}
	return id, item, valid
}

func loadBatch(path string) ([]json.RawMessage, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, core.Fail("FILE_IO", "Cannot open the batch file.", false)
	}
	defer f.Close()
	const limit = 32 << 20
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, core.Fail("FILE_IO", "Cannot read the batch file.", false)
	}
	invalid := core.Fail("INVALID_BATCH", "Provide a UTF-8 JSON manifest up to 32 MiB with a nonempty items array.", false)
	if len(b) > limit || !utf8.Valid(b) {
		return nil, invalid
	}
	doc, ok := object(b)
	var items []json.RawMessage
	if !ok || len(doc) != 1 || json.Unmarshal(doc["items"], &items) != nil || len(items) == 0 {
		return nil, invalid
	}
	return items, nil
}

func (a *app) batch() *cobra.Command {
	f := &synthFlags{}
	cmd := command("batch <file>", "Generate MP3 files from a JSON manifest", []string{"b"}, exactly(1), func(cmd *cobra.Command, args []string) error {
		a.alwaysJSON = true
		items, e := loadBatch(args[0])
		if e != nil {
			return e
		}
		config, e := a.load(cmd, false)
		if e != nil {
			return e
		}
		flags := f.values(cmd)
		// Validate non-item-specific settings independently of language/voice defaults.
		common := map[string]string{}
		for k, v := range flags {
			common[k] = v
		}
		common["language"], common["voice"] = "en-US", "kore"
		settings, e := config.Resolve(common, nil)
		if e != nil {
			return e
		}

		report := manifest{Items: make([]itemResult, len(items)), Total: len(items), DryRun: f.dryRun}
		ids := map[string]int{}
		entries := make([]batchItem, len(items))
		for i, raw := range items {
			id, item, ok := decodeItem(raw)
			entries[i] = item
			report.Items[i].ID = id
			ids[id]++
			if !ok {
				report.Items[i].Error = core.Fail("INVALID_ITEM", "Expected a nonempty string ID, text, and optional language and voice strings.", false)
			}
		}
		eng := &core.Engine{Provider: a.backend, Warn: a.warn}
		jobs := make(chan int)
		var wg sync.WaitGroup
		workers := min(settings.Jobs, len(items))
		for range workers {
			wg.Go(func() {
				for i := range jobs {
					item, r := entries[i], &report.Items[i]
					s, err := config.Resolve(flags, item.Settings)
					if err == nil {
						var result core.Generation
						result, err = eng.Generate(cmd.Context(), s, item.Text, core.Options{DryRun: f.dryRun, NoCache: f.noCache})
						if err == nil {
							r.Success = true
							r.Data = &result
						}
					}
					if err != nil {
						r.Error = core.Public(err)
					}
				}
			})
		}
		for i := range items {
			r := &report.Items[i]
			if r.Error != nil {
				continue
			}
			if ids[r.ID] != 1 {
				r.Error = core.Fail("DUPLICATE_ID", "Every batch ID must be unique.", false)
				continue
			}
			select {
			case <-cmd.Context().Done():
				r.Error = core.Public(cmd.Context().Err())
			case jobs <- i:
			}
		}
		close(jobs)
		wg.Wait()
		for _, r := range report.Items {
			if r.Success {
				report.Succeeded++
			} else {
				report.Failed++
			}
		}
		a.result, a.human = report, fmt.Sprintf("Batch: %d succeeded, %d failed.", report.Succeeded, report.Failed)
		if report.Failed > 0 {
			a.code = 3
		}
		return nil
	})
	bindSynth(cmd, f, true)
	return cmd
}
