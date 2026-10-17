package ratelimit

import (
	"testing"
	"time"
)

func TestLimiterWindow(t *testing.T) {
	l := New(3, time.Minute)
	now := time.Now()
	for i := 0; i < 3; i++ {
		if !l.allowAt("ip", now) {
			t.Fatalf("evento %d deveria passar", i)
		}
	}
	if l.allowAt("ip", now) {
		t.Fatal("quarto evento deveria ser bloqueado")
	}
	if !l.allowAt("outro", now) {
		t.Fatal("outra chave não deveria ser afetada")
	}
	if !l.allowAt("ip", now.Add(61*time.Second)) {
		t.Fatal("após a janela deveria liberar")
	}
}
