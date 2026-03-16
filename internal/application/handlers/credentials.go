package handlers

import (
	"io"
	"net/http"

	"github.com/DataDog/jsonapi"
	"github.com/gin-gonic/gin"
	"github.com/mcasperson/MockGitRepo/internal/domain/configuration"
	"github.com/mcasperson/MockGitRepo/internal/domain/logging"
	"github.com/mcasperson/MockGitRepo/internal/domain/model"
	"github.com/mcasperson/MockGitRepo/internal/domain/responses"
	"github.com/mcasperson/MockGitRepo/internal/infrastructure"
	"go.uber.org/zap"
)

func Credentials(c *gin.Context) {
	serviceToken := c.GetHeader("X_MOCKGIT_SERVICE_API_KEY")
	if serviceToken != configuration.GetServiceToken() {
		c.String(401, "Unauthorized")
		return
	}

	body, err := io.ReadAll(c.Request.Body)

	if err != nil {
		logging.Logger.Error("Failed to read request body", zap.Error(err))
		c.IndentedJSON(http.StatusBadRequest, responses.GenerateError("Failed to read request body", err))
		return
	}

	var credentials model.Credentials
	err = jsonapi.Unmarshal(body, &credentials)

	if err != nil {
		logging.Logger.Error("Failed to parse request body", zap.Error(err))
		c.IndentedJSON(http.StatusBadRequest, responses.GenerateError("Failed to parse request body", err))
		return
	}

	err = infrastructure.SaveCredentials(credentials.Id, credentials.Password)

	if err != nil {
		logging.Logger.Error("Failed to persist credentials", zap.Error(err))
		c.IndentedJSON(http.StatusInternalServerError, responses.GenerateError("Failed to persist credentials", err))
		return
	}

	c.Status(http.StatusOK)
}
