package sign6

import (
	"encoding/hex"
	"testing"
)

func TestPlayerLadonVector(t *testing.T) {
	nonce, _ := hex.DecodeString("a80a146e")
	got := PlayerLadon("8662", 1793618443, nonce)
	want := "qAoUbk6lh8IKV1YvouVJI+9w+oZcE7E7QtXpCtbLQVdksvxp"
	if got != want {
		t.Fatalf("PlayerLadon mismatch\n got %s\nwant %s", got, want)
	}
}
