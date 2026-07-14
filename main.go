package main

import (
	"bufio"
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"

	"github.com/ericbaek/musecat-backend-core/coreapp"
)

func main() {
	loadEnvFile(".env")
	dataDir := os.Getenv("PB_DATA_DIR")
	if dataDir == "" {
		dataDir = "./pb_data"
	}
	envName := strings.ToLower(strings.TrimSpace(os.Getenv("APP_ENV")))
	if envName == "" {
		envName = "development"
	}
	autoMigrate := envName != "production"
	if override := strings.TrimSpace(os.Getenv("PB_AUTOMIGRATE")); override != "" {
		if value, err := strconv.ParseBool(override); err == nil {
			autoMigrate = value
		}
	}
	app := pocketbase.NewWithConfig(pocketbase.Config{DefaultDataDir: dataDir})
	coreapp.Configure(app, autoMigrate)
	if err := app.Start(); err != nil {
		log.Fatal(err)
	}
}

func registerDocumentationRoutes(se *core.ServeEvent) {
	coreapp.RegisterDocumentationRoutes(se)
}

func loadEnvFile(path string) {
	file, err := os.Open(path)
	if err != nil {
		return
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if index := strings.Index(line, "="); index != -1 {
			key := strings.TrimSpace(line[:index])
			value := strings.Trim(strings.TrimSpace(line[index+1:]), `"'`)
			if key != "" {
				os.Setenv(key, value)
			}
		}
	}
}
