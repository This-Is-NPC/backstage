package engine

import (
	"crypto/rand"
	"encoding/hex"

	"github.com/This-Is-NPC/backstage/internal/scene"
)

var newStateGeneration = func() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return ""
	}
	return hex.EncodeToString(b[:])
}

// NewStateGeneration returns a fresh id for one producing run.
func NewStateGeneration() string {
	return newStateGeneration()
}

// ValidStateGeneration is a 32-digit lowercase hex id.
func ValidStateGeneration(id string) bool {
	if len(id) != 32 {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		if c >= '0' && c <= '9' || c >= 'a' && c <= 'f' {
			continue
		}
		return false
	}
	return true
}

func ensureProducerGeneration(s *scene.Scene, opts *Options) {
	if opts.StateGeneration != "" {
		return
	}
	if s != nil && s.EndGroup() != "" {
		opts.StateGeneration = NewStateGeneration()
	}
}
