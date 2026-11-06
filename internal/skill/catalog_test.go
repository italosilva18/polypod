package skill

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeCatalogFixture(t *testing.T, root, slug, description string) string {
	t.Helper()
	dir := filepath.Join(root, slug)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: " + slug + "\ndescription: " + description + "\n---\n\n# " + slug + "\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestCatalogFindUsesExistingAdaptation(t *testing.T) {
	base := t.TempDir()
	source := filepath.Join(base, "catalog")
	active := filepath.Join(base, ".polypod", "skills")
	writeCatalogFixture(t, source, "github", "Use GitHub issues e PRs")
	writeCatalogFixture(t, active, "polyclaw_github", "Integração nativa do GitHub")
	catalog := NewLocalCatalog(source, active, NewRegistry())
	entries, err := catalog.Find(context.Background(), "github", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Slug != "github" || entries[0].Status != "active_adapter" || entries[0].ActiveAs != "polyclaw_github" {
		t.Fatalf("duplicated or unavailable adapter: %+v", entries)
	}
	result, err := catalog.Install(context.Background(), "github")
	if err != nil || result.Status != "already_active" || result.Name != "polyclaw_github" {
		t.Fatalf("adaptation ignored: %+v, %v", result, err)
	}
	if _, err := os.Stat(filepath.Join(active, "github")); !os.IsNotExist(err) {
		t.Fatalf("raw duplicate was installed: %v", err)
	}
}

func TestCatalogInstallStagesThenRegistersCompleteSkill(t *testing.T) {
	base := t.TempDir()
	source := filepath.Join(base, "catalog")
	active := filepath.Join(base, ".polypod", "skills")
	sourceDir := writeCatalogFixture(t, source, "newskill", "Nova capacidade local")
	if err := os.Mkdir(filepath.Join(sourceDir, "references"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "references", "guide.md"), []byte("guia"), 0644); err != nil {
		t.Fatal(err)
	}
	reg := NewRegistry()
	notified := 0
	reg.SetOnChange(func(name string, exists bool) {
		if name == "newskill" && exists {
			notified++
		}
	})
	catalog := NewLocalCatalog(source, active, reg)
	installed, err := catalog.Install(context.Background(), "newskill")
	if err != nil || installed.Status != "installed" || len(installed.SourceSHA256) != 64 {
		t.Fatalf("install: %+v, %v", installed, err)
	}
	if notified != 1 {
		t.Fatalf("registry callback count=%d", notified)
	}
	loaded, ok := reg.Get("newskill")
	if !ok || !strings.Contains(loaded.Description, "Nova capacidade") {
		t.Fatal("skill not registered")
	}
	if data, err := os.ReadFile(filepath.Join(installed.Path, "references", "guide.md")); err != nil || string(data) != "guia" {
		t.Fatalf("reference missing: %q, %v", data, err)
	}
	metaData, err := os.ReadFile(filepath.Join(installed.Path, ".skill-origin.json"))
	if err != nil {
		t.Fatal(err)
	}
	var meta originMetadata
	if err := json.Unmarshal(metaData, &meta); err != nil || meta.Catalog != "polyclaw" || meta.Slug != "newskill" || meta.SourceSHA256 != installed.SourceSHA256 {
		t.Fatalf("provenance: %+v, %v", meta, err)
	}
	parsed, err := NewFSSkillLoader(active).LoadAll(context.Background())
	if err != nil || len(parsed) != 1 || parsed[0].Frontmatter.Name != "newskill" {
		t.Fatalf("watcher loader cannot see install: %+v, %v", parsed, err)
	}
	stages, err := filepath.Glob(filepath.Join(filepath.Dir(active), ".polypod-skill-stage-*"))
	if err != nil || len(stages) != 0 {
		t.Fatalf("staging leaked: %v, %v", stages, err)
	}
	again, err := catalog.Install(context.Background(), "newskill")
	if err != nil || again.Status != "already_installed" {
		t.Fatalf("repeat install: %+v, %v", again, err)
	}
}

func TestCatalogRejectsTraversalAndSymlinkResources(t *testing.T) {
	base := t.TempDir()
	source := filepath.Join(base, "catalog")
	active := filepath.Join(base, ".polypod", "skills")
	sourceDir := writeCatalogFixture(t, source, "unsafe", "Unsafe fixture")
	secret := filepath.Join(base, "private.txt")
	if err := os.WriteFile(secret, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(sourceDir, "linked.txt")); err != nil {
		t.Fatal(err)
	}
	catalog := NewLocalCatalog(source, active, NewRegistry())
	for _, slug := range []string{"../private", "unsafe/../private", ".hidden", "UPPER", ""} {
		if _, err := catalog.Install(context.Background(), slug); err == nil {
			t.Fatalf("accepted invalid slug %q", slug)
		}
	}
	if _, err := catalog.Install(context.Background(), "unsafe"); err == nil || !strings.Contains(err.Error(), "link simbólico") {
		t.Fatalf("accepted symlink resource: %v", err)
	}
	if _, err := os.Stat(filepath.Join(active, "unsafe")); !os.IsNotExist(err) {
		t.Fatalf("partial install visible: %v", err)
	}
	stages, err := filepath.Glob(filepath.Join(filepath.Dir(active), ".polypod-skill-stage-*"))
	if err != nil || len(stages) != 0 {
		t.Fatalf("staging leaked: %v, %v", stages, err)
	}
}

func TestCatalogRejectsInvalidManifestAndSymlinkDestination(t *testing.T) {
	base := t.TempDir()
	source := filepath.Join(base, "catalog")
	active := filepath.Join(base, ".polypod", "skills")
	dir := writeCatalogFixture(t, source, "broken", "Broken fixture")
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: wrong\ndescription: x\n---\n"), 0644); err != nil {
		t.Fatal(err)
	}
	catalog := NewLocalCatalog(source, active, NewRegistry())
	if _, err := catalog.Install(context.Background(), "broken"); err == nil {
		t.Fatal("accepted wrong frontmatter name")
	}
	writeCatalogFixture(t, source, "good", "Good fixture")
	if err := os.MkdirAll(active, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(dir, filepath.Join(active, "good")); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.Install(context.Background(), "good"); err == nil {
		t.Fatal("followed symlink destination")
	}
}

func TestCatalogFindReturnsValidEntriesWithMalformedNeighbor(t *testing.T) {
	base := t.TempDir()
	source := filepath.Join(base, "catalog")
	active := filepath.Join(base, "active")
	writeCatalogFixture(t, source, "good", "Good fixture")
	bad := writeCatalogFixture(t, source, "bad", "Bad fixture")
	if err := os.WriteFile(filepath.Join(bad, "SKILL.md"), []byte("---\nname: bad\ndescription: [\n---\n"), 0644); err != nil {
		t.Fatal(err)
	}
	entries, err := NewLocalCatalog(source, active, nil).Find(context.Background(), "*", 20)
	if err == nil || len(entries) != 1 || entries[0].Slug != "good" {
		t.Fatalf("partial results lost: %+v, %v", entries, err)
	}
}
