package utils

import "testing"

func TestObfuscateChannelTokenIsStable(t *testing.T) {
	token1 := ObfuscateChannelToken("Example Channel HD")
	token2 := ObfuscateChannelToken("Example Channel HD")

	if token1 != token2 {
		t.Fatalf("expected stable token, got %q and %q", token1, token2)
	}

	if token1 == "Example Channel HD" {
		t.Fatalf("expected token to differ from channel name")
	}
}

func TestObfuscateChannelTokenDiffersByChannel(t *testing.T) {
	token1 := ObfuscateChannelToken("Example Channel HD")
	token2 := ObfuscateChannelToken("Example Channel SD")

	if token1 == token2 {
		t.Fatalf("expected different channels to produce different tokens")
	}
}
