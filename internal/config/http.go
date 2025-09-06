package config

import (
	"fmt"
	"path"
	"strings"

	"github.com/knadh/koanf"
	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/providers/structs"
	"go.uber.org/zap"

	"github.com/snapp-incubator/proksi/internal/logging"
)

var (
	// k is the global koanf instance. Use "." as the key path delimiter.
	k = koanf.New(".")

	// HTTP is the config for Proksi HTTP
	HTTP *HTTPConfig

	// ComputedConfigs contains pre-computed route configurations for fast runtime lookup
	ComputedConfigs *ComputedRouteConfigs
)

var defaultHTTP = HTTPConfig{
	Bind: "0.0.0.0:9090",
	Metrics: metric{
		Enabled: true,
		Bind:    "0.0.0.0:9001",
	},
	StorageType: "stdout",
	Elasticsearch: Elasticsearch{
		Addresses:              []string{"::9200"},
		Username:               "",
		Password:               "",
		CloudID:                "",
		APIKey:                 "",
		ServiceToken:           "",
		CertificateFingerprint: "",
	},
	Upstreams: struct {
		Main httpUpstream `koanf:"main"`
		Test httpUpstream `koanf:"test"`
	}{
		Main: httpUpstream{Address: "127.0.0.1:8080"},
		Test: httpUpstream{Address: "127.0.0.1:8081"},
	},
	Worker: worker{
		Count:     50,
		QueueSize: 2048,
	},

	// New per-route configuration defaults
	GlobalConfig: GlobalConfig{
		CompareHeaders:  true,
		SkipHeaders:     []string{},
		StoreReqBody:    false,
		StoreRespBodies: true,
		SkipJSONPaths:   []string{},
		TestProbability: 100,
	},
	RouteConfigs: make(map[string]RouteConfig),
	SkipRoutes:   []string{},

	// Legacy fields for backward compatibility
	SkipJSONPaths:      []string{},
	TestProbability:    100,
	LogResponsePayload: true,
	CompareHeaders:     true,
}

// HTTPConfig represent config of the Proksi HTTP.
type HTTPConfig struct {
	Bind          string        `koanf:"bind"`
	Metrics       metric        `koanf:"metrics"`
	StorageType   string        `koanf:"storage_type"` // Storage backend type: "elasticsearch" or "stdout"
	Elasticsearch Elasticsearch `koanf:"elasticsearch"`
	Upstreams     struct {
		Main httpUpstream `koanf:"main"`
		Test httpUpstream `koanf:"test"`
	} `koanf:"upstreams"`
	Worker worker `koanf:"worker"`

	// New per-route configuration
	GlobalConfig GlobalConfig           `koanf:"global_config"`
	RouteConfigs map[string]RouteConfig `koanf:"route_configs"`
	SkipRoutes   []string               `koanf:"skip_routes"`

	// Legacy fields for backward compatibility - deprecated but still supported
	SkipJSONPaths      []string `koanf:"skip_json_paths"`      // Deprecated: use GlobalConfig.SkipJSONPaths
	TestProbability    uint64   `koanf:"test_probability"`     // Deprecated: use GlobalConfig.TestProbability
	LogResponsePayload bool     `koanf:"log_response_payload"` // Deprecated: use GlobalConfig.StoreRespBodies
	CompareHeaders     bool     `koanf:"compare_headers"`      // Deprecated: use GlobalConfig.CompareHeaders
}

type httpUpstream struct {
	Address string `koanf:"address"`
}

type worker struct {
	Count     uint `koanf:"count"`
	QueueSize uint `koanf:"queue_size"`
}

// RouteConfig represents per-route configuration overrides
type RouteConfig struct {
	CompareHeaders  bool     `koanf:"compare_headers"`   // Override global compare headers setting
	SkipHeaders     []string `koanf:"skip_headers"`      // Headers to skip during comparison
	StoreReqBody    bool     `koanf:"store_req_body"`    // Store request body on differences
	StoreRespBodies bool     `koanf:"store_resp_bodies"` // Store response bodies on differences
	SkipJSONPaths   []string `koanf:"skip_json_paths"`   // Route-specific JSON paths to skip
	TestProbability uint64   `koanf:"test_probability"`  // Override global test probability for this route
}

// GlobalConfig represents global default configuration
type GlobalConfig struct {
	CompareHeaders  bool     `koanf:"compare_headers"`   // Default: true
	SkipHeaders     []string `koanf:"skip_headers"`      // Global headers to skip
	StoreReqBody    bool     `koanf:"store_req_body"`    // Default: false
	StoreRespBodies bool     `koanf:"store_resp_bodies"` // Default: true (current LogResponsePayload)
	SkipJSONPaths   []string `koanf:"skip_json_paths"`   // Global JSON paths to skip
	TestProbability uint64   `koanf:"test_probability"`  // Default: 100
}

// ComputedRouteConfigs contains pre-computed route configurations for fast runtime lookup
type ComputedRouteConfigs struct {
	// Pre-computed route configs: "GET:/api/users" -> merged config
	Routes map[string]RouteConfig
	
	// Pre-computed global config (with legacy migration applied)
	Global RouteConfig
	
	// Skip routes for fast lookup: "GET:/health" -> true
	SkipRoutes map[string]bool
}

// LoadHTTP function will load the file located in path and return the parsed config for ProksiHTTP. This function will panic on errors
func LoadHTTP(path string) *HTTPConfig {
	// LoadHTTP default config in the beginning
	err := k.Load(structs.Provider(defaultHTTP, "koanf"), nil)
	if err != nil {
		logging.L.Fatal("error in loading the default config", zap.Error(err))
	}

	// LoadHTTP YAML config and merge into the previously loaded config.
	err = k.Load(file.Provider(path), yaml.Parser())
	if err != nil {
		logging.L.Fatal("error in loading the config file", zap.Error(err))
	}

	var c HTTPConfig
	err = k.Unmarshal("", &c)
	if err != nil {
		logging.L.Fatal("error in unmarshalling the config file", zap.Error(err))
	}

	// Apply backward compatibility migrations
	c.migrateFromLegacyConfig()

	// Pre-compute route configurations for fast runtime lookup
	ComputedConfigs = c.PrecomputeRouteConfigs()

	HTTP = &c
	return &c
}

// migrateFromLegacyConfig migrates legacy configuration fields to new GlobalConfig structure
func (c *HTTPConfig) migrateFromLegacyConfig() {
	// Only migrate if the legacy fields are set and global config fields are not explicitly set

	// Migrate CompareHeaders
	if c.CompareHeaders != c.GlobalConfig.CompareHeaders {
		c.GlobalConfig.CompareHeaders = c.CompareHeaders
	}

	// Migrate TestProbability
	if c.TestProbability != 0 && c.TestProbability != c.GlobalConfig.TestProbability {
		c.GlobalConfig.TestProbability = c.TestProbability
	}

	// Migrate LogResponsePayload to StoreRespBodies
	if !c.LogResponsePayload && c.GlobalConfig.StoreRespBodies {
		c.GlobalConfig.StoreRespBodies = c.LogResponsePayload
	}

	// Migrate SkipJSONPaths
	if len(c.SkipJSONPaths) > 0 && len(c.GlobalConfig.SkipJSONPaths) == 0 {
		c.GlobalConfig.SkipJSONPaths = c.SkipJSONPaths
	}
}

// FormatRoute formats HTTP method and path into a route string
func FormatRoute(method, path string) string {
	return fmt.Sprintf("%s:%s", strings.ToUpper(method), path)
}

// ParseRoute parses a route string into method and path components
func ParseRoute(route string) (method, path string) {
	parts := strings.SplitN(route, ":", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	// If no method specified, assume wildcard
	return "*", route
}

// MatchRoute checks if a request route matches a configured route pattern
func MatchRoute(requestRoute, configRoute string) bool {
	requestMethod, requestPath := ParseRoute(requestRoute)
	configMethod, configPath := ParseRoute(configRoute)

	// Check method match (wildcard "*" matches any method)
	if configMethod != "*" && configMethod != requestMethod {
		return false
	}

	// Check path match
	return matchPath(requestPath, configPath)
}

// matchPath checks if a request path matches a configured path pattern
func matchPath(requestPath, configPath string) bool {
	// Exact match
	if requestPath == configPath {
		return true
	}

	// Wildcard match - supports trailing /* pattern
	if strings.HasSuffix(configPath, "/*") {
		prefix := strings.TrimSuffix(configPath, "/*")
		return strings.HasPrefix(requestPath, prefix)
	}

	// Path pattern match using Go's path.Match
	matched, _ := path.Match(configPath, requestPath)
	return matched
}


// PrecomputeRouteConfigs creates pre-computed route configurations for fast runtime lookup
func (c *HTTPConfig) PrecomputeRouteConfigs() *ComputedRouteConfigs {
	computed := &ComputedRouteConfigs{
		Routes:     make(map[string]RouteConfig),
		SkipRoutes: make(map[string]bool),
	}

	// Pre-compute global config (with legacy migration applied)
	computed.Global = RouteConfig{
		CompareHeaders:  c.GlobalConfig.CompareHeaders,
		SkipHeaders:     append([]string{}, c.GlobalConfig.SkipHeaders...),
		StoreReqBody:    c.GlobalConfig.StoreReqBody,
		StoreRespBodies: c.GlobalConfig.StoreRespBodies,
		SkipJSONPaths:   append([]string{}, c.GlobalConfig.SkipJSONPaths...),
		TestProbability: c.GlobalConfig.TestProbability,
	}

	// Pre-compute skip routes for fast lookup
	for _, skipRoute := range c.SkipRoutes {
		computed.SkipRoutes[skipRoute] = true
	}

	// Pre-compute route-specific configurations
	for routePattern, routeConfig := range c.RouteConfigs {
		// Start with global config as base
		mergedConfig := RouteConfig{
			CompareHeaders:  computed.Global.CompareHeaders,
			SkipHeaders:     append([]string{}, computed.Global.SkipHeaders...),
			StoreReqBody:    computed.Global.StoreReqBody,
			StoreRespBodies: computed.Global.StoreRespBodies,
			SkipJSONPaths:   append([]string{}, computed.Global.SkipJSONPaths...),
			TestProbability: computed.Global.TestProbability,
		}

		// Override with route-specific config
		mergedConfig.CompareHeaders = routeConfig.CompareHeaders
		mergedConfig.StoreReqBody = routeConfig.StoreReqBody
		mergedConfig.StoreRespBodies = routeConfig.StoreRespBodies
		
		if len(routeConfig.SkipHeaders) > 0 {
			mergedConfig.SkipHeaders = append(mergedConfig.SkipHeaders, routeConfig.SkipHeaders...)
		}
		if len(routeConfig.SkipJSONPaths) > 0 {
			mergedConfig.SkipJSONPaths = append(mergedConfig.SkipJSONPaths, routeConfig.SkipJSONPaths...)
		}
		if routeConfig.TestProbability > 0 {
			mergedConfig.TestProbability = routeConfig.TestProbability
		}

		// Store the pre-computed config
		computed.Routes[routePattern] = mergedConfig
	}

	return computed
}

// GetRouteConfig returns pre-computed route configuration for runtime lookup
func GetRouteConfig(route string) RouteConfig {
	// Check if route exists in pre-computed configs
	if config, exists := ComputedConfigs.Routes[route]; exists {
		return config
	}
	
	// Return global config if no specific route config found
	return ComputedConfigs.Global
}

// IsRouteSkipped checks if a route should be skipped using pre-computed lookup
func IsRouteSkipped(route string) bool {
	return ComputedConfigs.SkipRoutes[route]
}
