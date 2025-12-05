// Package main is the entry point for the application.
//
// @title My API
// @version 1.0
// @description API description here
//
// @host localhost:8080
// @BasePath /api/v1
// @schemes http https
package main

import "github.com/yourorg/myapp/cmd/myapp/cmd"

func main() {
	cmd.Execute()
}
