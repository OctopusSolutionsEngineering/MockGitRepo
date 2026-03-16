package infrastructure

import "os"

func GetStorageConnectionString() string {
	return os.Getenv("AzureWebJobsStorage")
}
