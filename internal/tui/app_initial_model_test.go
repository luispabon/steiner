package tui

import (
	"testing"
)

func TestSetInitialModelSeedsModel(t *testing.T) {
	t.Parallel()
	app, err := NewApp(Config{
		Model:             "small",
		CurrentModelAlias: "small",
		ModelContexts:     map[string]int{"small": 8192, "large": 65536},
	})
	if err != nil {
		t.Fatal(err)
	}
	app.SetInitialModel("large", "http://local/v1", "local")

	m := newModel(app.cfg, nil)
	if m.primaryModel != "large" || m.currentModelAlias != "large" {
		t.Fatalf("model state = (%q, %q), want large", m.primaryModel, m.currentModelAlias)
	}
	if m.sidebar.model != "large" || m.sidebar.contextBudget != 65536 {
		t.Fatalf("sidebar = (model %q, budget %d), want (large, 65536)", m.sidebar.model, m.sidebar.contextBudget)
	}
	if m.sidebar.provider != "http://local/v1" || m.sidebar.providerName != "local" {
		t.Fatalf("sidebar provider = (%q, %q), want (http://local/v1, local)", m.sidebar.provider, m.sidebar.providerName)
	}
}

func TestHandleSessionLoadedMsgAppliesRestoredModel(t *testing.T) {
	t.Parallel()
	app, err := NewApp(Config{
		Model:             "small",
		CurrentModelAlias: "small",
		ModelContexts:     map[string]int{"small": 8192, "large": 65536},
		ModelBaseURLs:     map[string]string{"large": "http://large/v1"},
		ModelProviderNames: map[string]string{
			"large": "local",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	m := newModel(app.cfg, nil)
	model, _ := m.handleSessionLoadedMsg(sessionLoadedMsg{alias: "large"})
	m = model.(*Model)
	if m.primaryModel != "large" || m.currentModelAlias != "large" {
		t.Fatalf("model state = (%q, %q), want large", m.primaryModel, m.currentModelAlias)
	}
	if m.sidebar.contextBudget != 65536 || m.sidebar.provider != "http://large/v1" {
		t.Fatalf("sidebar = (budget %d, provider %q), want (65536, http://large/v1)", m.sidebar.contextBudget, m.sidebar.provider)
	}
}
