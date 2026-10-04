package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/hdmain/goincus/internal/apicli"
	"github.com/hdmain/goincus/internal/models"
)

type cliFlags struct {
	url        string
	key        string
	configPath string
	jsonOut    bool
}

func parseCLIFlags(fs *flag.FlagSet, args []string) (*cliFlags, []string) {
	f := &cliFlags{}
	fs.StringVar(&f.url, "url", "", "API base URL (env GOINCUS_URL, default http://127.0.0.1:9603)")
	fs.StringVar(&f.key, "key", "", "API key (env GOINCUS_API_KEY, or first key from config)")
	fs.StringVar(&f.configPath, "config", "", "config.yaml for API key (env GOINCUS_CONFIG, default /etc/goincus/config.yaml)")
	fs.BoolVar(&f.jsonOut, "json", false, "print raw JSON")
	_ = fs.Parse(args)
	return f, fs.Args()
}

func newAPIClient(f *cliFlags) (*apicli.Client, error) {
	return apicli.New(apicli.Options{
		URL:        f.url,
		APIKey:     f.key,
		ConfigPath: f.configPath,
	})
}

func runHealth(args []string) int {
	fs := flag.NewFlagSet("health", flag.ExitOnError)
	f, _ := parseCLIFlags(fs, args)
	cli, err := newAPIClient(f)
	if err != nil {
		return cliErr(err)
	}
	h, err := cli.Health(context.Background())
	if err != nil {
		return cliErr(err)
	}
	if f.jsonOut {
		return printJSON(h)
	}
	fmt.Printf("status=%s database=%s redis=%s incus=%s\n", h.Status, h.Database, h.Redis, h.Incus)
	for k, v := range h.Checks {
		fmt.Printf("  %s: %s\n", k, v)
	}
	if h.Status != "ok" {
		return 1
	}
	return 0
}

func runList(args []string) int {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	f, _ := parseCLIFlags(fs, args)
	cli, err := newAPIClient(f)
	if err != nil {
		return cliErr(err)
	}
	list, err := cli.ListInstances(context.Background())
	if err != nil {
		return cliErr(err)
	}
	if f.jsonOut {
		return printJSON(list)
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tSTATUS\tCPU\tMEM\tDISK\tPORTS\tID")
	for _, inst := range list {
		fmt.Fprintf(w, "%s\t%s\t%s\t%dMB\t%dGB\t%s\t%s\n",
			inst.Name, inst.Status, formatCPU(inst.CPUCores), inst.MemoryMB, inst.StorageGB,
			apicli.PortRange(&inst), shortID(inst.ID.String()))
	}
	_ = w.Flush()
	return 0
}

func runGet(args []string) int {
	name, flagArgs := splitNameAndFlags(args)
	fs := flag.NewFlagSet("get", flag.ExitOnError)
	f, rest := parseCLIFlags(fs, flagArgs)
	if name == "" && len(rest) > 0 {
		name = rest[0]
	}
	if name == "" {
		fmt.Fprintln(os.Stderr, "usage: goincus get <name-or-id> [flags]")
		return 2
	}
	cli, err := newAPIClient(f)
	if err != nil {
		return cliErr(err)
	}
	inst, err := cli.GetInstance(context.Background(), name)
	if err != nil {
		return cliErr(err)
	}
	if f.jsonOut {
		return printJSON(inst)
	}
	printInstance(inst)
	return 0
}

func runCreate(args []string) int {
	// Support both: create NAME [flags] and create [flags] NAME
	name, flagArgs := splitNameAndFlags(args)
	fs := flag.NewFlagSet("create", flag.ExitOnError)
	cpu := fs.Float64("cpu", 0, "CPU cores, supports fractions e.g. 0.5 (0 = server default)")
	mem := fs.Int("memory", 0, "memory MiB (0 = server default)")
	disk := fs.Int("disk", 0, "disk GiB (0 = server default)")
	image := fs.String("image", "", "image alias (default from server config)")
	wait := fs.Bool("wait", true, "wait until running with ports")
	timeout := fs.Duration("timeout", 10*time.Minute, "max wait with -wait")
	f, rest := parseCLIFlags(fs, flagArgs)
	if name == "" && len(rest) > 0 {
		name = rest[0]
	}
	if name == "" {
		fmt.Fprintln(os.Stderr, "usage: goincus create <name> [-cpu 0.5|1|2] [-memory MiB] [-disk GiB] [-image ALIAS] [-wait=false]")
		return 2
	}
	cli, err := newAPIClient(f)
	if err != nil {
		return cliErr(err)
	}
	req := models.CreateInstanceRequest{
		Name:      name,
		CPUCores:  *cpu,
		MemoryMB:  *mem,
		StorageGB: *disk,
		Image:     *image,
	}
	inst, err := cli.CreateInstance(context.Background(), req)
	if err != nil {
		return cliErr(err)
	}
	if *wait {
		fmt.Fprintf(os.Stderr, "creating %s …\n", inst.Name)
		inst, err = cli.WaitRunning(context.Background(), inst.ID.String(), *timeout)
		if err != nil {
			if inst != nil && !f.jsonOut {
				printInstance(inst)
			}
			return cliErr(err)
		}
	}
	if f.jsonOut {
		return printJSON(inst)
	}
	printInstance(inst)
	if p := apicli.SSHPort(inst); p > 0 {
		host := sshHostHint(f.url, cli.BaseURL)
		fmt.Printf("\nssh root@%s -p %d\n", host, p)
		if inst.RootPassword != "" {
			fmt.Printf("password: %s\n", inst.RootPassword)
		}
	}
	return 0
}

func runDelete(args []string) int {
	name, flagArgs := splitNameAndFlags(args)
	fs := flag.NewFlagSet("delete", flag.ExitOnError)
	yes := fs.Bool("y", false, "do not prompt")
	f, rest := parseCLIFlags(fs, flagArgs)
	if name == "" && len(rest) > 0 {
		name = rest[0]
	}
	if name == "" {
		fmt.Fprintln(os.Stderr, "usage: goincus delete <name-or-id> [-y]")
		return 2
	}
	if !*yes {
		fmt.Fprintf(os.Stderr, "delete %s? [y/N] ", name)
		var ans string
		_, _ = fmt.Scanln(&ans)
		if strings.ToLower(strings.TrimSpace(ans)) != "y" && strings.ToLower(strings.TrimSpace(ans)) != "yes" {
			fmt.Fprintln(os.Stderr, "aborted")
			return 1
		}
	}
	cli, err := newAPIClient(f)
	if err != nil {
		return cliErr(err)
	}
	if err := cli.DeleteInstance(context.Background(), name); err != nil {
		return cliErr(err)
	}
	fmt.Printf("deleted %s\n", name)
	return 0
}

func runStart(args []string) int   { return runAction("start", args) }
func runStop(args []string) int    { return runAction("stop", args) }
func runRestart(args []string) int { return runAction("restart", args) }
func runRepair(args []string) int  { return runAction("repair", args) }

func runAction(action string, args []string) int {
	name, flagArgs := splitNameAndFlags(args)
	fs := flag.NewFlagSet(action, flag.ExitOnError)
	f, rest := parseCLIFlags(fs, flagArgs)
	if name == "" && len(rest) > 0 {
		name = rest[0]
	}
	if name == "" {
		fmt.Fprintf(os.Stderr, "usage: goincus %s <name-or-id>\n", action)
		return 2
	}
	cli, err := newAPIClient(f)
	if err != nil {
		return cliErr(err)
	}
	var inst *models.Instance
	switch action {
	case "start":
		inst, err = cli.StartInstance(context.Background(), name)
	case "stop":
		inst, err = cli.StopInstance(context.Background(), name)
	case "restart":
		inst, err = cli.RestartInstance(context.Background(), name)
	case "repair":
		inst, err = cli.RepairInstance(context.Background(), name)
	default:
		return cliErr(fmt.Errorf("unknown action %s", action))
	}
	if err != nil {
		return cliErr(err)
	}
	if f.jsonOut {
		return printJSON(inst)
	}
	printInstance(inst)
	return 0
}

func runSSHInfo(args []string) int {
	name, flagArgs := splitNameAndFlags(args)
	fs := flag.NewFlagSet("ssh", flag.ExitOnError)
	hostFlag := fs.String("host", "", "SSH host to print (default: host from -url, or GOINCUS_SSH_HOST)")
	f, rest := parseCLIFlags(fs, flagArgs)
	if name == "" && len(rest) > 0 {
		name = rest[0]
	}
	if name == "" {
		fmt.Fprintln(os.Stderr, "usage: goincus ssh <name-or-id> [-host IP]")
		return 2
	}
	cli, err := newAPIClient(f)
	if err != nil {
		return cliErr(err)
	}
	inst, err := cli.GetInstance(context.Background(), name)
	if err != nil {
		return cliErr(err)
	}
	port := apicli.SSHPort(inst)
	if port == 0 {
		return cliErr(fmt.Errorf("instance has no ports yet (status=%s)", inst.Status))
	}
	host := *hostFlag
	if host == "" {
		host = os.Getenv("GOINCUS_SSH_HOST")
	}
	if host == "" {
		host = sshHostHint(f.url, cli.BaseURL)
	}
	if f.jsonOut {
		return printJSON(map[string]any{
			"host":          host,
			"port":          port,
			"user":          "root",
			"password":      inst.RootPassword,
			"command":       fmt.Sprintf("ssh root@%s -p %d", host, port),
			"name":          inst.Name,
			"id":            inst.ID.String(),
			"ports":         apicli.PortRange(inst),
		})
	}
	fmt.Printf("ssh root@%s -p %d\n", host, port)
	if inst.RootPassword != "" {
		fmt.Printf("password: %s\n", inst.RootPassword)
	}
	fmt.Printf("ports: %s\n", apicli.PortRange(inst))
	return 0
}

func printInstance(inst *models.Instance) {
	fmt.Printf("name:       %s\n", inst.Name)
	fmt.Printf("id:         %s\n", inst.ID)
	fmt.Printf("incus:      %s\n", inst.IncusName)
	fmt.Printf("status:     %s\n", inst.Status)
	fmt.Printf("image:      %s\n", inst.Image)
	fmt.Printf("resources:  %s CPU / %d MiB / %d GiB\n", formatCPU(inst.CPUCores), inst.MemoryMB, inst.StorageGB)
	fmt.Printf("ports:      %s (%d mapped)\n", apicli.PortRange(inst), len(inst.Ports))
	if p := apicli.SSHPort(inst); p > 0 {
		fmt.Printf("ssh_port:   %d\n", p)
	}
	if inst.RootPassword != "" {
		fmt.Printf("password:   %s\n", inst.RootPassword)
	}
	if inst.ErrorMessage != "" {
		fmt.Printf("error:      %s\n", inst.ErrorMessage)
	}
}

func printJSON(v any) int {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return cliErr(err)
	}
	return 0
}

func cliErr(err error) int {
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	return 1
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func formatCPU(v float64) string {
	if v == float64(int(v)) {
		return fmt.Sprintf("%g", v)
	}
	return fmt.Sprintf("%.2g", v)
}

// splitNameAndFlags pulls a leading positional name so flags may follow it.
func splitNameAndFlags(args []string) (name string, flagArgs []string) {
	if len(args) == 0 {
		return "", nil
	}
	if !strings.HasPrefix(args[0], "-") {
		return args[0], args[1:]
	}
	return "", args
}

func sshHostHint(flagURL, baseURL string) string {
	if h := os.Getenv("GOINCUS_SSH_HOST"); h != "" {
		return h
	}
	u := flagURL
	if u == "" {
		u = baseURL
	}
	u = strings.TrimPrefix(u, "http://")
	u = strings.TrimPrefix(u, "https://")
	if i := strings.IndexByte(u, '/'); i >= 0 {
		u = u[:i]
	}
	if i := strings.IndexByte(u, ':'); i >= 0 {
		u = u[:i]
	}
	if u == "" || u == "127.0.0.1" || u == "localhost" || u == "0.0.0.0" {
		return "127.0.0.1"
	}
	return u
}
