package bskyapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

type sessionCredentials struct {
	Service    string
	DID        string
	Handle     string
	PDSURL     string
	AccessJWT  string
	RefreshJWT string
}

type persistedSessionState struct {
	Accounts       []persistedAccount       `json:"accounts"`
	CurrentAccount *persistedCurrentAccount `json:"currentAccount"`
}

type persistedAccount struct {
	Service    string `json:"service"`
	DID        string `json:"did"`
	Handle     string `json:"handle"`
	PDSURL     string `json:"pdsUrl"`
	AccessJWT  string `json:"accessJwt"`
	RefreshJWT string `json:"refreshJwt"`
	Active     *bool  `json:"active"`
}

type persistedCurrentAccount struct {
	DID string `json:"did"`
}

type persistedRoot struct {
	Session persistedSessionState `json:"session"`
}

func parsePersistedSession(raw []byte) (sessionCredentials, error) {
	var root persistedRoot
	if err := json.Unmarshal(raw, &root); err != nil {
		return sessionCredentials{}, fmt.Errorf("parse persisted Bluesky session: %w", err)
	}
	if len(root.Session.Accounts) == 0 {
		return sessionCredentials{}, errors.New("persisted Bluesky session has no accounts")
	}

	did := ""
	if root.Session.CurrentAccount != nil {
		did = strings.TrimSpace(root.Session.CurrentAccount.DID)
	}
	if did == "" {
		for _, account := range root.Session.Accounts {
			if account.Active != nil && *account.Active {
				if did != "" {
					return sessionCredentials{}, errors.New("persisted Bluesky session has multiple active accounts")
				}
				did = strings.TrimSpace(account.DID)
			}
		}
	}
	if did == "" {
		return sessionCredentials{}, errors.New("persisted Bluesky session has no unambiguous current account")
	}

	var selected *persistedAccount
	for index := range root.Session.Accounts {
		account := &root.Session.Accounts[index]
		if strings.TrimSpace(account.DID) == did {
			if selected != nil {
				return sessionCredentials{}, fmt.Errorf("persisted Bluesky session has duplicate account %q", did)
			}
			selected = account
		}
	}
	if selected == nil {
		return sessionCredentials{}, fmt.Errorf("persisted Bluesky session has no account for current DID %q", did)
	}

	credentials := sessionCredentials{
		Service:    strings.TrimSpace(selected.Service),
		DID:        did,
		Handle:     strings.TrimSpace(selected.Handle),
		PDSURL:     strings.TrimSpace(selected.PDSURL),
		AccessJWT:  strings.TrimSpace(selected.AccessJWT),
		RefreshJWT: strings.TrimSpace(selected.RefreshJWT),
	}
	if credentials.Service == "" {
		return sessionCredentials{}, fmt.Errorf("persisted Bluesky account %q has no service", did)
	}
	if credentials.AccessJWT == "" && credentials.RefreshJWT == "" {
		return sessionCredentials{}, fmt.Errorf("persisted Bluesky account %q has no usable session token", did)
	}
	return credentials, nil
}
