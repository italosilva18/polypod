package memory

import "testing"

func TestRelevantRequiresEvidenceEvenForCriticalMemory(t *testing.T) {
	m := Memory{Topic: "database", Content: "Postgres port 5432", Priority: 2}
	if ok, _ := m.IsRelevant("gardening"); ok {
		t.Fatal("priority alone must not inject unrelated memory")
	}
	if ok, _ := m.IsRelevant(""); ok {
		t.Fatal("empty query must not match everything")
	}
	if ok, _ := m.IsRelevant("postgres?"); !ok {
		t.Fatal("punctuation should not prevent lexical recall")
	}
}
