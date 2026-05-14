package store

import (
	"context"
	"testing"
)

func TestApproveGitHubAuthUserAndBindOAuthProfile(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := openTestStore(t)

	approved, created, err := st.ApproveGitHubAuthUser(ctx, "@Darron")
	if err != nil {
		t.Fatalf("ApproveGitHubAuthUser: %v", err)
	}
	if !created {
		t.Fatalf("expected first approval to create a row")
	}
	if approved.Provider != authProviderGitHub || approved.GitHubUsername != "Darron" || approved.GitHubUsernameNormalized != "darron" || approved.GitHubID != "" {
		t.Fatalf("unexpected approved user: %#v", approved)
	}

	lookup, found, err := st.GetGitHubAuthUserByUsername(ctx, "DARRON")
	if err != nil {
		t.Fatalf("GetGitHubAuthUserByUsername: %v", err)
	}
	if !found || lookup.ID != approved.ID {
		t.Fatalf("expected case-insensitive username lookup to find approved user, found=%v user=%#v", found, lookup)
	}

	if _, found, err := st.GetGitHubAuthUserByID(ctx, "12345"); err != nil {
		t.Fatalf("GetGitHubAuthUserByID before bind: %v", err)
	} else if found {
		t.Fatalf("did not expect github id lookup before first login bind")
	}

	bound, err := st.UpdateGitHubAuthUserFromOAuth(ctx, approved.ID, GitHubAuthProfile{
		GitHubID:       "12345",
		GitHubUsername: "dArRoN",
		Email:          "darron@example.test",
		Name:           "Darron",
		AvatarURL:      "https://avatars.example.test/darron.png",
	})
	if err != nil {
		t.Fatalf("UpdateGitHubAuthUserFromOAuth: %v", err)
	}
	if bound.GitHubID != "12345" || bound.GitHubUsername != "dArRoN" || bound.GitHubUsernameNormalized != "darron" || bound.Email != "darron@example.test" || bound.Name != "Darron" || bound.AvatarURL == "" || bound.LastLoginAt == "" {
		t.Fatalf("unexpected bound user: %#v", bound)
	}

	lookup, found, err = st.GetGitHubAuthUserByID(ctx, "12345")
	if err != nil {
		t.Fatalf("GetGitHubAuthUserByID after bind: %v", err)
	}
	if !found || lookup.ID != approved.ID {
		t.Fatalf("expected github id lookup to find bound user, found=%v user=%#v", found, lookup)
	}

	again, created, err := st.ApproveGitHubAuthUser(ctx, "darron")
	if err != nil {
		t.Fatalf("ApproveGitHubAuthUser again: %v", err)
	}
	if created || again.GitHubID != "12345" {
		t.Fatalf("expected repeated approval to preserve bound id, created=%v user=%#v", created, again)
	}
}
