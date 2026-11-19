package msf

import (
	"context"
	"testing"
	"time"
)

func TestProbeLive(t *testing.T) {
	c := New("http://127.0.0.1:55553", "msf", "{{SECRET_ylpeszii}}")
	c.http.Timeout = 40 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	if err := c.Login(ctx); err != nil {
		t.Fatalf("login: %v", err)
	}
	t.Log("login OK")
	s, err := c.Sessions(ctx)
	t.Logf("sessions=%+v err=%v", s, err)
	j, err := c.Jobs(ctx)
	t.Logf("jobs=%v err=%v", j, err)
}
