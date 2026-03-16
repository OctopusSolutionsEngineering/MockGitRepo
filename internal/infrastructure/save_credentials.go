package infrastructure

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/data/aztables"
	"github.com/mcasperson/MockGitRepo/internal/domain/security"
)

// credentialEntity represents a row in the Azure Storage Table.
type credentialEntity struct {
	aztables.Entity
	Username string
	Password string
}

// SaveCredentials saves the given username and password to the "credentials"
// Azure Storage Table. The connection string is read from the
// AzureWebJobsStorage environment variable via GetStorageConnectionString.
// The PartitionKey is "credentials" and the RowKey is the username.
func SaveCredentials(username, password string) error {
	connStr := GetStorageConnectionString()
	if connStr == "" {
		return fmt.Errorf("AzureWebJobsStorage environment variable is not set")
	}

	client, err := aztables.NewServiceClientFromConnectionString(connStr, nil)
	if err != nil {
		return fmt.Errorf("failed to create Azure Tables service client: %w", err)
	}

	tableClient := client.NewClient("credentials")

	// Ensure the table exists (no-op if it already does).
	if _, err = tableClient.CreateTable(context.Background(), nil); err != nil {
		var respErr *azcore.ResponseError
		if !errors.As(err, &respErr) || respErr.ErrorCode != string(aztables.TableAlreadyExists) {
			return fmt.Errorf("failed to create credentials table: %w", err)
		}
	}

	entity := credentialEntity{
		Entity: aztables.Entity{
			PartitionKey: "credentials",
			RowKey:       username,
		},
		Username: username,
		Password: security.GetSha256(password),
	}

	data, err := json.Marshal(entity)
	if err != nil {
		return fmt.Errorf("failed to marshal credential entity: %w", err)
	}

	_, err = tableClient.UpsertEntity(context.Background(), data, &aztables.UpsertEntityOptions{
		UpdateMode: aztables.UpdateModeReplace,
	})
	if err != nil {
		return fmt.Errorf("failed to upsert credential entity: %w", err)
	}

	return nil
}
