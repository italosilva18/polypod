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
