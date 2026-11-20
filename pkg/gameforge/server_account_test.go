package gameforge

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFindServer(t *testing.T) {
	servers := []Server{
		{Name: "Universum 68", Number: 68, Language: "pl"},
		{Name: "Kerberos", Number: 189, Language: "pl"},
		{Name: "Xolotlan", Number: 279, Language: "pl"},
		{Name: "Yi–SWAN", Number: 280, Language: "pl"}, // with en-dash
		{Name: "Universe 1", Number: 1, Language: "en"},
		{Name: "Xolotlan", Number: 279, Language: "en"},
	}

	t.Run("exact match", func(t *testing.T) {
		s, found := FindServer("Xolotlan", "pl", servers)
		assert.True(t, found)
		assert.Equal(t, int64(279), s.Number)
	})

	t.Run("case-insensitive and trimmed", func(t *testing.T) {
		s, found := FindServer("  xolotlan  ", "pl", servers)
		assert.True(t, found)
		assert.Equal(t, int64(279), s.Number)
	})

	t.Run("en-dash normalization (Yi-SWAN with regular dash)", func(t *testing.T) {
		s, found := FindServer("Yi-SWAN", "pl", servers)
		assert.True(t, found)
		assert.Equal(t, int64(280), s.Number)

		// lowercase with regular dash
		s2, found2 := FindServer("yi-swan", "pl", servers)
		assert.True(t, found2)
		assert.Equal(t, int64(280), s2.Number)
	})

	t.Run("number match by raw number string", func(t *testing.T) {
		s, found := FindServer("279", "pl", servers)
		assert.True(t, found)
		assert.Equal(t, "Xolotlan", s.Name)
		assert.Equal(t, int64(279), s.Number)
	})

	t.Run("number match by s-prefix (e.g. s280 or s280-pl)", func(t *testing.T) {
		s, found := FindServer("s280", "pl", servers)
		assert.True(t, found)
		assert.Equal(t, int64(280), s.Number)

		s2, found2 := FindServer("s280-pl", "pl", servers)
		assert.True(t, found2)
		assert.Equal(t, int64(280), s2.Number)
	})

	t.Run("not found", func(t *testing.T) {
		_, found := FindServer("NonExistent", "pl", servers)
		assert.False(t, found)
	})
}

func TestFindAccountHelpfulErrorMessageOnMerge(t *testing.T) {
	servers := []Server{
		{Name: "Kerberos", Number: 189, Language: "pl"},
		{Name: "Xolotlan", Number: 279, Language: "pl"},
	}

	// Player's account was migrated to Xolotlan 279
	accounts := []Account{
		{
			ID:   123456,
			Name: "CommanderX",
			Server: struct {
				Language string
				Number   int64
			}{Language: "pl", Number: 279},
		},
	}

	// If bot tries to login to Kerberos (old exodus server):
	_, _, err := FindAccount("Kerberos", "pl", 0, accounts, servers)
	assert.Error(t, err)
	assert.True(t, errors.Is(err, ErrAccountNotFound))
	assert.Contains(t, err.Error(), "Xolotlan (279, pl)")
	assert.Contains(t, err.Error(), "CommanderX")
}
