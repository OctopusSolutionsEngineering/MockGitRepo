package configuration

import "os"

const (
	defaultPort = "8080"
)

func GetPort() string {
	port := os.Getenv("PORT")
	if port == "" {
		port = defaultPort
	}
	return port
}
