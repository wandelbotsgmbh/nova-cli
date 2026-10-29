package environment

import (
	"log/slog"
	"os"
	"strings"
)

func GetBasePath() string {
	basePath := strings.TrimSpace(os.Getenv("BASE_PATH"))
	if len(basePath) > 0 {
		if basePath[0] != '/' {
			basePath = "/" + basePath
		}
		if basePath[len(basePath)-1] == '/' {
			basePath = basePath[:len(basePath)-1]
		}
	}
	return basePath
}

func GetLogLevel() slog.Level {
	logLevel := strings.ToLower(strings.TrimSpace(os.Getenv("LOG_LEVEL")))
	switch logLevel {
	case "debug":
		return slog.LevelDebug.Level()
	case "warn":
		return slog.LevelWarn.Level()
	case "error":
		return slog.LevelError.Level()
	default:
		return slog.LevelInfo.Level()
	}
}

func GetNovaApiPath() string {
	novaApi := strings.TrimSpace(os.Getenv("NOVA_API"))
	if len(novaApi) > 0 && !strings.HasSuffix(novaApi, "/") {
		novaApi += "/"
	}
	if !strings.HasSuffix(novaApi, "http") {
		novaApi = "http://" + novaApi
	}

	return novaApi + "api/v1/"
}

func GetNovaCellName() string {
	cellId := strings.TrimSpace(os.Getenv("CELL_NAME"))
	if cellId == "" {
		return "cell"
	}
	return cellId
}

func GetAccessToken() string {
	return strings.TrimSpace(os.Getenv("ACCESS_TOKEN"))
}
