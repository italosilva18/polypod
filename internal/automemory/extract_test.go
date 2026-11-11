package automemory

import "testing"

func TestParseExtractedMemoriesFiltersSecretsAndInvalidItems(t *testing.T) {
	input := `[
      {"type":"preference","content":"O usuario prefere respostas em portugues.","tags":["Idioma"],"importance":4},
      {"type":"fact","content":"A senha do banco e segredo123","tags":["credencial"],"importance":5},
      {"type":"other","content":"Este tipo nao deve passar.","importance":3},
      {"type":"preference","content":"O usuario prefere respostas em portugues.","importance":2}
    ]`
	got, err := ParseExtractedMemories(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("memorias validas = %d, esperado 1: %+v", len(got), got)
	}
	if got[0].Tags[0] != "idioma" || got[0].Importance != 4 {
		t.Fatalf("normalizacao inesperada: %+v", got[0])
	}
}

func TestParseExtractedMemoriesAcceptsFencedJSON(t *testing.T) {
	got, err := ParseExtractedMemories("```json\n[{\"type\":\"context\",\"content\":\"O projeto principal se chama Polypod.\",\"importance\":9}]\n```")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Importance != 5 {
		t.Fatalf("resultado inesperado: %+v", got)
	}
}

func TestParseExtractedMemoriesDistinguishesEmptyFromInvalid(t *testing.T) {
	if got, err := ParseExtractedMemories("[]"); err != nil || len(got) != 0 {
		t.Fatalf("explicitly empty extraction: count=%d err=%v", len(got), err)
	}
	for _, raw := range []string{
		`null`,
		`[{"type":"other","content":"Synthetic invalid fact."}]`,
	} {
		if _, err := ParseExtractedMemories(raw); err == nil {
			t.Fatalf("invalid model output was accepted")
		}
	}
	if got, err := ParseExtractedMemories(`[{"type":"preference","content":"A senha do teste e segredo123."}]`); err != nil || len(got) != 0 {
		t.Fatalf("sensitive-only output should be discarded without retry: count=%d err=%v", len(got), err)
	}
}
