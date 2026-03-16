package configuration

import (
	"os"

	"github.com/mcasperson/MockGitRepo/internal/domain/logging"
	"github.com/mcasperson/MockGitRepo/internal/domain/random"
	"go.uber.org/zap"
)

var randomToken string = random.GenerateRandomString(32)

func GetServiceToken() string {
	serviceToken := os.Getenv("GIT_SERVICE_TOKEN")
	if serviceToken == "" {
		serviceToken = randomToken
		logging.Logger.Info("Generated service token", zap.String("token", serviceToken))
	}
	return serviceToken
}
