package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	HTTPAddr             string
	ExternalAPIKeys      map[string]string
	InternalJWTSecret    string
	DefaultRequestLimit  int
	ProviderTimeout      time.Duration
	RedisAddr            string
	RedisPassword        string
	RedisDB              int
	EnableScraping       bool
	PBARecaptchaToken    string
	CapSolverAPIKey      string
	PBASiteKey           string
	VehicleProfileAPIURL string
	VehicleProfileAPIKey string
	CarsXEAPIKey         string
	VehicleDBPath        string
	GeminiAPIKey         string
	DatabaseURL          string
	MPAccessToken        string
	BackendURL           string
	FrontendURL          string
}

func loadDotEnv() {
	data, err := os.ReadFile(".env")
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if k != "" {
			os.Setenv(k, v)
		}
	}
}

func Load() Config {
	loadDotEnv()
	keys := parseMapEnv("EXTERNAL_API_KEYS")
	if len(keys) == 0 {
		keys["local-dev"] = "external-secret-1"
	}

	return Config{
		HTTPAddr:             getHTTPAddr(),
		ExternalAPIKeys:      keys,
		InternalJWTSecret:    getEnv("INTERNAL_JWT_SECRET", "change-me"),
		DefaultRequestLimit:  getEnvAsInt("DEFAULT_REQUEST_LIMIT", 60),
		ProviderTimeout:      getEnvAsDuration("PROVIDER_TIMEOUT", 30*time.Second),
		RedisAddr:            getEnv("REDIS_ADDR", "localhost:6379"),
		RedisPassword:        getEnv("REDIS_PASSWORD", ""),
		RedisDB:              getEnvAsInt("REDIS_DB", 0),
		EnableScraping:       getEnvAsBool("ENABLE_SCRAPING", true),
		PBARecaptchaToken:    getEnv("PBA_RECAPTCHA_TOKEN", ""),
		CapSolverAPIKey:      getEnv("CAPSOLVER_API_KEY", ""),
		PBASiteKey:           getEnv("PBA_SITE_KEY", ""),
		VehicleProfileAPIURL: getEnv("VEHICLE_PROFILE_API_URL", ""),
		VehicleProfileAPIKey: getEnv("VEHICLE_PROFILE_API_KEY", ""),
		CarsXEAPIKey:         getEnv("CARSXE_API_KEY", ""),
		VehicleDBPath:        getEnv("VEHICLE_DB_PATH", "./vehicle_profiles.db"),
		GeminiAPIKey:         getEnv("GEMINI_API_KEY", ""),
		DatabaseURL:          getEnv("DATABASE_URL", ""),
		MPAccessToken:        getEnv("MP_ACCESS_TOKEN", ""),
		BackendURL:           getEnv("BACKEND_URL", "http://localhost:8080"),
		FrontendURL:          getEnv("FRONTEND_URL", "http://localhost:5173"),
	}
}

func getHTTPAddr() string {
	if addr := os.Getenv("HTTP_ADDR"); addr != "" {
		return addr
	}
	if port := os.Getenv("PORT"); port != "" {
		return ":" + port
	}
	return ":8080"
}

func getEnv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return fallback
}

func getEnvAsInt(key string, fallback int) int {
	raw, ok := os.LookupEnv(key)
	if !ok {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return n
}

func getEnvAsDuration(key string, fallback time.Duration) time.Duration {
	raw, ok := os.LookupEnv(key)
	if !ok {
		return fallback
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return fallback
	}
	return d
}

func getEnvAsBool(key string, fallback bool) bool {
	raw, ok := os.LookupEnv(key)
	if !ok {
		return fallback
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func parseMapEnv(key string) map[string]string {
	out := map[string]string{}
	raw, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(raw) == "" {
		return out
	}
	for _, pair := range strings.Split(raw, ",") {
		p := strings.SplitN(strings.TrimSpace(pair), ":", 2)
		if len(p) != 2 {
			continue
		}
		name := strings.TrimSpace(p[0])
		secret := strings.TrimSpace(p[1])
		if name != "" && secret != "" {
			out[name] = secret
		}
	}
	return out
}
