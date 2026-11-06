package ai

import (
	"context"
	"sync"
	"testing"
)

func TestSkillNamesAreIsolatedPerRequest(t *testing.T) {
	c := &Client{}
	c.SetSkillNames([]string{"global"})
	ctxA := withRequestSkillNames(context.Background(), []string{"first"})
	ctxB := withRequestSkillNames(context.Background(), []string{"second"})
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(3)
		go func() {
			defer wg.Done()
			if got := c.skillNamesFor(ctxA); len(got) != 1 || got[0] != "first" {
				t.Errorf("request A got %v", got)
			}
		}()
		go func() {
			defer wg.Done()
			if got := c.skillNamesFor(ctxB); len(got) != 1 || got[0] != "second" {
				t.Errorf("request B got %v", got)
			}
		}()
		go func() {
			defer wg.Done()
			c.SetSkillNames([]string{"changed"})
		}()
	}
	wg.Wait()
}
