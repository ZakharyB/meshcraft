package config

import (
	"path/filepath"
	"testing"
)

func validWorld() WorldConfig {
	return WorldConfig{ID: "w1", Edition: "java", Dir: "/tmp/w1", Port: 25565, MaxRAMMB: 1024}
}

func TestValidateRejectsBedrockForNow(t *testing.T) {
	c := Default("/tmp/x")
	w := validWorld()
	w.Edition = "bedrock"
	c.Worlds = []WorldConfig{w}
	if err := c.Validate(); err == nil {
		t.Fatal("bedrock must be rejected until Phase 8")
	}
}

func TestValidateRejectsDuplicatePort(t *testing.T) {
	c := Default("/tmp/x")
	w2 := validWorld()
	w2.ID = "w2"
	c.Worlds = []WorldConfig{validWorld(), w2}
	if err := c.Validate(); err == nil {
		t.Fatal("duplicate ports must be rejected")
	}
}

func TestSaveLoadRoundtrip(t *testing.T) {
	dir := t.TempDir()
	c := Default(dir)
	c.Worlds = []WorldConfig{validWorld()}
	p := filepath.Join(dir, "config.json")
	if err := c.Save(p); err != nil {
		t.Fatal(err)
	}
	got, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Worlds) != 1 || got.Worlds[0].JarPath != "paper.jar" || got.Worlds[0].BackupKeep != 5 {
		t.Fatalf("defaults not applied: %+v", got.Worlds)
	}
}
