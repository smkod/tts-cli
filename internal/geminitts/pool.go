package geminitts

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/smkod/tts-cli/internal/core"
)

const (
	defaultRateLimitCooldown = 60 * time.Second
	defaultQuotaCooldown     = 1 * time.Hour
)

type keyCooldown struct {
	CooldownUntil time.Time `json:"cooldown_until"`
	Reason        string    `json:"reason,omitempty"`
	Transient     bool      `json:"transient,omitempty"`
}

type keyStateFile struct {
	Cooldowns map[string]keyCooldown `json:"cooldowns"`
}

// KeyStatus describes a configured key and its current availability.
type KeyStatus struct {
	Name          string     `json:"name"`
	Tier          string     `json:"tier"`
	MaskedKey     string     `json:"masked_key"`
	Source        string     `json:"source"`
	Available     bool       `json:"available"`
	CooldownUntil *time.Time `json:"cooldown_until,omitempty"`
	LastReason    string     `json:"last_reason,omitempty"`
}

// KeyPool manages thread-safe selection and cooldown tracking across Gemini API keys.
type KeyPool struct {
	mu        sync.Mutex
	now       func() time.Time
	cooldowns map[string]keyCooldown
	loaded    map[string]bool
}

// NewKeyPool creates a new KeyPool.
func NewKeyPool() *KeyPool {
	return &KeyPool{
		now:       time.Now,
		cooldowns: map[string]keyCooldown{},
		loaded:    map[string]bool{},
	}
}

// StatePath returns the path where cooldown state is persisted.
func StatePath(configDir, cacheDir string) string {
	dir := cacheDir
	if dir == "" {
		dir = configDir
	}
	if dir == "" {
		if d, err := os.UserCacheDir(); err == nil {
			dir = filepath.Join(d, "tts")
		}
	}
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "gemini-key-state.json")
}

// Acquire returns the next available key for the requested tier (auto, free, or paid),
// skipping any keys in cooldown or in the exclude set.
func (p *KeyPool) Acquire(keys []KeyEntry, tier, statePath string, exclude map[string]bool) (KeyEntry, bool, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ensureLoadedLocked(statePath)
	now := p.now()

	ordered := filterAndOrderKeys(keys, tier)
	if len(ordered) == 0 {
		return KeyEntry{}, false, false
	}
	anyCooled := false
	for _, k := range ordered {
		if exclude != nil && exclude[k.Key] {
			continue
		}
		if cd, ok := p.cooldowns[KeyID(k.Key)]; ok && now.Before(cd.CooldownUntil) {
			anyCooled = true
			continue
		}
		return k, true, anyCooled
	}
	return KeyEntry{}, false, anyCooled
}

// MarkExhausted puts a key into cooldown and persists the state when statePath is non-empty.
func (p *KeyPool) MarkExhausted(key string, duration time.Duration, reason string, transient bool, statePath string) {
	if duration <= 0 {
		if transient {
			duration = defaultRateLimitCooldown
		} else {
			duration = defaultQuotaCooldown
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ensureLoadedLocked(statePath)
	now := p.now()
	p.pruneExpiredLocked(now)
	p.cooldowns[KeyID(key)] = keyCooldown{
		CooldownUntil: now.Add(duration),
		Reason:        reason,
		Transient:     transient,
	}
	p.saveLocked(statePath)
}

// ResetCooldowns clears all in-memory and persisted key cooldowns.
func (p *KeyPool) ResetCooldowns(statePath string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cooldowns = map[string]keyCooldown{}
	p.loaded = map[string]bool{}
	if statePath == "" {
		return nil
	}
	err := os.Remove(statePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// Statuses returns availability information for all configured keys.
func (p *KeyPool) Statuses(keys []KeyEntry, statePath string) []KeyStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ensureLoadedLocked(statePath)
	now := p.now()
	out := make([]KeyStatus, 0, len(keys))
	for _, k := range keys {
		st := KeyStatus{
			Name:      k.Name,
			Tier:      k.Tier,
			MaskedKey: MaskKey(k.Key),
			Source:    k.Source,
			Available: true,
		}
		if cd, ok := p.cooldowns[KeyID(k.Key)]; ok && now.Before(cd.CooldownUntil) {
			until := cd.CooldownUntil
			st.Available = false
			st.CooldownUntil = &until
			st.LastReason = cd.Reason
		}
		out = append(out, st)
	}
	return out
}

func (p *KeyPool) ensureLoadedLocked(statePath string) {
	if statePath == "" || p.loaded[statePath] {
		return
	}
	p.loaded[statePath] = true
	b, err := os.ReadFile(statePath)
	if err != nil || len(b) == 0 {
		return
	}
	var doc keyStateFile
	if json.Unmarshal(b, &doc) != nil || doc.Cooldowns == nil {
		return
	}
	now := p.now()
	for id, cd := range doc.Cooldowns {
		if now.Before(cd.CooldownUntil) {
			p.cooldowns[id] = cd
		}
	}
}

func (p *KeyPool) pruneExpiredLocked(now time.Time) {
	for id, cd := range p.cooldowns {
		if !now.Before(cd.CooldownUntil) {
			delete(p.cooldowns, id)
		}
	}
}

func (p *KeyPool) saveLocked(statePath string) {
	if statePath == "" {
		return
	}
	_ = writePrivateJSON(statePath, keyStateFile{Cooldowns: p.cooldowns})
}

func filterAndOrderKeys(keys []KeyEntry, tier string) []KeyEntry {
	switch tier {
	case core.KeyTierFree:
		var free []KeyEntry
		for _, k := range keys {
			if k.Tier == TierFree {
				free = append(free, k)
			}
		}
		return free
	case core.KeyTierPaid:
		var paid []KeyEntry
		for _, k := range keys {
			if k.Tier == TierPaid {
				paid = append(paid, k)
			}
		}
		return paid
	default:
		var free, paid []KeyEntry
		for _, k := range keys {
			if k.Tier == TierPaid {
				paid = append(paid, k)
			} else {
				free = append(free, k)
			}
		}
		return append(free, paid...)
	}
}
