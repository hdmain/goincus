package setup

const (
	DefaultConfigPath = "/etc/goincus/config.yaml"
	DefaultBinaryPath = "/usr/local/bin/goincus"
	DefaultUnitPath   = "/etc/systemd/system/goincus.service"
	MigrationsDir     = "/usr/share/goincus/migrations"
)

// Options controls goincus init behavior.
type Options struct {
	ConfigPath  string
	SkipInstall bool
	Force       bool
}

// Result is printed after a successful init.
type Result struct {
	ConfigPath string
	APIKeys    []string
	DBPassword string
	RedisPass  string
}
