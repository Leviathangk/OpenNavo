package interfacesync

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"syscall"
)

// ResumeCache stores translations validated per key, independently of final language-pack and lockfile writes.
type ResumeCache struct{ Directory string }
type ResumeEntry struct {
	SourceHash  string `json:"sourceHash"`
	EnglishHash string `json:"englishHash"`
	Translation string `json:"translation"`
	Batch       string `json:"batch"`
}
type resumeFile struct {
	Version int                               `json:"version"`
	Target  string                            `json:"target"`
	Entries map[string]map[string]ResumeEntry `json:"entries"`
}

func (c ResumeCache) path(target string) string {
	return filepath.Join(c.Directory, Hash(target)+".json")
}
func (c ResumeCache) read(target string) (resumeFile, error) {
	value := resumeFile{Version: 1, Target: target, Entries: map[string]map[string]ResumeEntry{}}
	data, err := os.ReadFile(c.path(target)) // #nosec G304 -- Filenames contain only target hashes; the command or test specifies the directory.
	if errors.Is(err, os.ErrNotExist) {
		return value, nil
	}
	if err != nil {
		return value, fmt.Errorf("cannot read resume cache: %s", target)
	}
	if json.Unmarshal(data, &value) != nil || value.Version != 1 || value.Target != target || value.Entries == nil {
		return value, fmt.Errorf("invalid resume cache: %s", target)
	}
	return value, nil
}
func (c ResumeCache) Save(batch Batch, output map[string]map[string]string) error {
	if err := os.MkdirAll(c.Directory, 0700); err != nil {
		return errors.New("cannot create resume cache")
	}
	lock, err := os.OpenFile(c.path(batch.Target)+".lock", os.O_CREATE|os.O_RDWR, 0600) // #nosec G304 -- Each target uses its own kernel lock and cannot overwrite concurrent targets.
	if err != nil {
		return errors.New("cannot open resume cache lock")
	}
	defer func() { _ = lock.Close() }()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return errors.New("cannot lock resume cache")
	}
	defer func() { _ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) }()
	value, err := c.read(batch.Target)
	if err != nil {
		return err
	}
	identity, err := json.Marshal(struct {
		Target          string
		Locales         []string
		Source, English map[string]string
	}{batch.Target, batch.Locales, batch.Source, batch.English})
	if err != nil {
		return errors.New("cannot encode resume batch")
	}
	for code, fields := range output {
		if value.Entries[code] == nil {
			value.Entries[code] = map[string]ResumeEntry{}
		}
		for key, translation := range fields {
			value.Entries[code][key] = ResumeEntry{SourceHash: Hash(batch.Source[key]), EnglishHash: Hash(batch.English[key]), Translation: translation, Batch: Hash(string(identity))}
		}
	}
	data, err := json.Marshal(value)
	if err != nil {
		return errors.New("cannot encode resume cache")
	}
	temporary, err := os.CreateTemp(c.Directory, ".resume-*")
	if err != nil {
		return errors.New("cannot create resume temporary file")
	}
	defer func() { _ = temporary.Close(); _ = os.Remove(temporary.Name()) }()
	if _, err = temporary.Write(data); err != nil {
		return errors.New("cannot write resume cache")
	}
	if err = temporary.Sync(); err != nil {
		return errors.New("cannot persist resume cache")
	}
	if err = temporary.Close(); err != nil {
		return errors.New("cannot close resume cache")
	}
	if err = os.Rename(temporary.Name(), c.path(batch.Target)); err != nil {
		return errors.New("cannot replace resume cache")
	}
	directory, err := os.Open(c.Directory) // #nosec G304 -- Sync only this tool's cache directory.
	if err != nil {
		return errors.New("cannot open resume directory")
	}
	defer func() { _ = directory.Close() }()
	if err = directory.Sync(); err != nil {
		return errors.New("cannot persist resume directory")
	}
	return nil
}
func (c ResumeCache) Restore(plan Plan, protected []string, glossary map[string]map[string]string) (Plan, []string, error) {
	restored := Plan{Targets: map[string]Target{}}
	var notices []string
	for _, name := range sortedTargetNames(plan) {
		target := plan.Targets[name]
		original := target
		target.Translations = map[string]map[string]string{}
		target.LocaleHashes = map[string]map[string]string{}
		for _, code := range translationLocales(name, target) {
			target.Translations[code] = map[string]string{}
			target.LocaleHashes[code] = map[string]string{}
			for key, value := range original.Translations[code] {
				target.Translations[code][key] = value
			}
			for key, value := range original.LocaleHashes[code] {
				target.LocaleHashes[code][key] = value
			}
		}
		cached, err := c.read(name)
		if err != nil {
			return Plan{}, nil, err
		}
		for _, code := range translationLocales(name, target) {
			fields := map[string]string{}
			pending := map[string]bool{}
			for key, value := range target.Translations[code] {
				if _, ok := target.Source[key]; ok && PendingReason(target, key, code) == "" {
					fields[key] = value
				}
			}
			for key, entry := range cached.Entries[code] {
				source, exists := target.Source[key]
				if !exists || entry.SourceHash != Hash(source) || entry.EnglishHash != Hash(target.English[key]) || PendingReason(target, key, code) == "" {
					continue
				}
				fields[key] = entry.Translation
				pending[key] = true
			}
			batch := Batch{Target: name, Locales: []string{code}, Source: map[string]string{}, English: map[string]string{}}
			for key := range fields {
				batch.Source[key] = target.Source[key]
				batch.English[key] = target.English[key]
			}
			var validation *ValidationError
			errors.As(ValidateEach(batch, map[string]map[string]string{code: fields}, protected, glossary), &validation)
			for _, key := range sortedKeys(fields) {
				if !pending[key] {
					continue
				}
				if validation != nil {
					reason := validation.Reason
					if validation.Keys != nil {
						reason = validation.Keys[code][key]
					}
					if reason != "" {
						notices = append(notices, "Cache validation failed; retranslating: "+reason)
						continue
					}
				}
				target.Translations[code][key] = fields[key]
				target.LocaleHashes[code][key] = Hash(target.Source[key])
				notices = append(notices, fmt.Sprintf("Cache restored: %s/%s/%s", name, code, key))
			}
		}
		restored.Targets[name] = target
	}
	return restored, notices, nil
}
func sortedTargetNames(plan Plan) []string {
	names := []string{}
	for name := range plan.Targets {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
