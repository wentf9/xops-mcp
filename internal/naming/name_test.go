package naming_test

import (
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/wentf9/xops-mcp/internal/naming"
)

func TestNamingContract(t *testing.T) {
	data, err := os.ReadFile("testdata/cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Label, Value string
		Valid        bool
	}
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	for _, test := range cases {
		t.Run(test.Label, func(t *testing.T) {
			err := naming.Validate(test.Value)
			if (err == nil) != test.Valid {
				t.Fatalf("%q: valid=%v, want %v", test.Value, err == nil, test.Valid)
			}
			if err != nil && !errors.Is(err, naming.ErrInvalid) {
				t.Fatal(err)
			}
		})
	}
	if err := naming.Validate(string([]byte{'a', 0xff})); !errors.Is(err, naming.ErrInvalid) {
		t.Fatal("invalid UTF-8 accepted")
	}
}
