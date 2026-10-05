package adminauth

import (
	"encoding/json"
	"os"
	"testing"
)

func TestPasswordUTF8ByteContract(t *testing.T) {
	data, err := os.ReadFile("testdata/passwords.json")
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
			if got := validPassword(test.Value); got != test.Valid {
				t.Fatalf("%d UTF-8 bytes: valid=%v, want %v", len(test.Value), got, test.Valid)
			}
		})
	}
	for _, value := range []string{"password\x00invalid", "password\ninvalid", "password\rinvalid"} {
		if validPassword(value) {
			t.Fatal("password containing NUL or a line break accepted")
		}
	}
}
