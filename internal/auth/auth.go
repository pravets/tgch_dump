// Package auth provides helpers for building a TDLib client with interactive
// CLI-based user authentication.
package auth

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/pravets/tgch_dump/internal/config"
	"github.com/zelenin/go-tdlib/client"
)

// NewClient creates an authenticated TDLib client using CLI prompts.
// If cfg.Telegram.Phone is non-empty it is used automatically; otherwise the
// user is prompted interactively.
func NewClient(cfg *config.Config) (*client.Client, error) {
	// Ensure database directories exist.
	if err := os.MkdirAll(cfg.Telegram.DatabaseDir, 0700); err != nil {
		return nil, fmt.Errorf("create database dir: %w", err)
	}
	if err := os.MkdirAll(cfg.Telegram.FilesDir, 0700); err != nil {
		return nil, fmt.Errorf("create files dir: %w", err)
	}

	tdlibParams := &client.SetTdlibParametersRequest{
		UseTestDc:           false,
		DatabaseDirectory:   cfg.Telegram.DatabaseDir,
		FilesDirectory:      cfg.Telegram.FilesDir,
		UseFileDatabase:     true,
		UseChatInfoDatabase: true,
		UseMessageDatabase:  true,
		UseSecretChats:      false,
		ApiId:               cfg.Telegram.APIID,
		ApiHash:             cfg.Telegram.APIHash,
		SystemLanguageCode:  "en",
		DeviceModel:         "tgch_dump",
		SystemVersion:       "1.0",
		ApplicationVersion:  "1.0",
	}

	authorizer := client.ClientAuthorizer(tdlibParams)

	// Launch the interactive prompts in a separate goroutine.
	go interactCLI(authorizer.State, authorizer.PhoneNumber, authorizer.Code, authorizer.Password, cfg.Telegram.Phone)

	tdClient, err := client.NewClient(
		authorizer,
		client.WithLogVerbosity(&client.SetLogVerbosityLevelRequest{
			NewVerbosityLevel: 0, // suppress most TDLib logs
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("tdlib authorize: %w", err)
	}

	return tdClient, nil
}

// interactCLI drives the clientAuthorizer channel-based state machine via
// stdin / the pre-configured phone number.
func interactCLI(
	state <-chan client.AuthorizationState,
	phoneNumber chan<- string,
	code chan<- string,
	password chan<- string,
	prefilledPhone string,
) {
	scanner := bufio.NewScanner(os.Stdin)

	for s := range state {
		switch s.AuthorizationStateType() {
		case client.TypeAuthorizationStateWaitPhoneNumber:
			phone := prefilledPhone
			if phone == "" {
				fmt.Print("Enter phone number (with country code, e.g. +79001234567): ")
				scanner.Scan()
				phone = strings.TrimSpace(scanner.Text())
			} else {
				fmt.Printf("Using phone number from config: %s\n", phone)
			}
			phoneNumber <- phone

		case client.TypeAuthorizationStateWaitCode:
			fmt.Print("Enter authentication code: ")
			scanner.Scan()
			code <- strings.TrimSpace(scanner.Text())

		case client.TypeAuthorizationStateWaitPassword:
			fmt.Print("Enter 2FA cloud password: ")
			scanner.Scan()
			password <- strings.TrimSpace(scanner.Text())

		case client.TypeAuthorizationStateReady:
			return
		}
	}
}
