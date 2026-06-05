package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoreSavesEncryptedProfilesAndLoadsWithMasterPassword(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tdb.enc")
	store := NewStore(path)
	vault := Vault{}
	vault.UpsertProfile(Profile{
		ID:       "mysql-local",
		Name:     "Local MySQL",
		Driver:   DriverMySQL,
		Host:     "127.0.0.1",
		Port:     3306,
		User:     "root",
		Password: "secret-password",
		Database: "app",
		ReadOnly: true,
	})

	if err := store.Save("master-password", vault); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	if string(raw) == "" || strings.Contains(string(raw), "secret-password") {
		t.Fatalf("encrypted file leaked plaintext password: %q", string(raw))
	}

	loaded, err := store.Load("master-password")
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	profile, ok := loaded.GetProfile("mysql-local")
	if !ok {
		t.Fatal("loaded vault missing mysql-local profile")
	}
	if profile.Password != "secret-password" || !profile.ReadOnly {
		t.Fatalf("loaded profile = %+v", profile)
	}
}

func TestStoreRejectsWrongMasterPassword(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tdb.enc")
	store := NewStore(path)
	if err := store.Save("correct", Vault{Profiles: []Profile{{ID: "redis", Driver: DriverRedis}}}); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}

	_, err := store.Load("wrong")
	if err == nil {
		t.Fatal("Load returned nil error for wrong master password")
	}
}

func TestVaultUpsertAndDeleteProfile(t *testing.T) {
	vault := Vault{}
	vault.UpsertProfile(Profile{ID: "redis-local", Name: "Redis", Driver: DriverRedis, Host: "127.0.0.1"})
	vault.UpsertProfile(Profile{ID: "redis-local", Name: "Redis dev", Driver: DriverRedis, Host: "localhost"})

	profile, ok := vault.GetProfile("redis-local")
	if !ok {
		t.Fatal("profile not found after upsert")
	}
	if profile.Name != "Redis dev" || profile.Host != "localhost" || len(vault.Profiles) != 1 {
		t.Fatalf("unexpected upsert result: %+v", vault.Profiles)
	}
	if !vault.DeleteProfile("redis-local") {
		t.Fatal("DeleteProfile returned false for existing profile")
	}
	if _, ok := vault.GetProfile("redis-local"); ok {
		t.Fatal("profile found after delete")
	}
}
