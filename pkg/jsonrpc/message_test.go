package jsonrpc

import (
	"encoding/json"
	"testing"
)

func TestIDMarshalRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		id   ID
		want string
	}{
		{"number", NewNumberID(42), `42`},
		{"string", NewStringID("abc"), `"abc"`},
		{"zero", NewNumberID(0), `0`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := json.Marshal(tt.id)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if string(got) != tt.want {
				t.Errorf("marshal = %s, want %s", got, tt.want)
			}
			var back ID
			if err := json.Unmarshal(got, &back); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if back != tt.id {
				t.Errorf("round trip = %+v, want %+v", back, tt.id)
			}
		})
	}
}

func TestIDStringDistinguishesNumberFromString(t *testing.T) {
	num := NewNumberID(1)
	str := NewStringID("1")
	if num.String() == str.String() {
		t.Errorf("numeric 1 and string \"1\" collided on key %q", num.String())
	}
}

func TestErrorImplementsError(t *testing.T) {
	var err error = &Error{Code: CodeMethodNotFound, Message: "nope"}
	if got, want := err.Error(), "jsonrpc: -32601 nope"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}
