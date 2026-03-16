package infrastructure

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/data/aztables"
	"github.com/mcasperson/MockGitRepo/internal/domain/security"
)

// TestCredentials returns true if the given username and password match the
// stored entry in the "credentials" Azure Storage Table, false otherwise.
func TestCredentials(username, password string) (bool, error) {
	connStr := GetStorageConnectionString()
	if connStr == "" {
		return false, fmt.Errorf("AzureWebJobsStorage environment variable is not set")
	}

	client, err := aztables.NewServiceClientFromConnectionString(connStr, nil)
	if err != nil {
		return false, fmt.Errorf("failed to create Azure Tables service client: %w", err)
	}

	tableClient := client.NewClient("credentials")

	resp, err := tableClient.GetEntity(context.Background(), "credentials", username, nil)
	if err != nil {
		var respErr *azcore.ResponseError
		if errors.As(err, &respErr) && respErr.StatusCode == http.StatusNotFound {
			return false, nil
		}
		return false, fmt.Errorf("failed to get credential entity: %w", err)
	}

	var entity credentialEntity
	if err = json.Unmarshal(resp.Value, &entity); err != nil {
		return false, fmt.Errorf("failed to unmarshal credential entity: %w", err)
	}

	return entity.Password == security.GetSha256(password), nil
}
