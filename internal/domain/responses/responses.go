package responses

import "github.com/DataDog/jsonapi"

func GenerateError(message string, err error) jsonapi.Error {
	return jsonapi.Error{
		Title:  message,
		Detail: err.Error(),
	}
}
