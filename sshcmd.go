package main

import (
	"fmt"
	"github.com/BurntSushi/toml"
	gssh "github.com/gliderlabs/ssh"
	"golang.org/x/crypto/ssh"
	"io"
	"log"
	"os"
	"os/exec"

	"sync/atomic"
)

const sshcmdhelp = "Run a command and pipe its input and output through ssh"

// Config describes all basic entries expected in a config toml.
type Config struct {
	// Host is the hostname to serve the ssh connection from.
	Host string
	// Port is the port to serve the ssh connection from.
	Port uint64
	// ServerKeyFilename is a list of private key path in pem format this ssh server uses.
	ServerKeyFilename []string
	// MaxNumberOfConnection is the number of connections after which we reject any further one.
	MaxNumberOfConnections int
}

// DefaultConfig creates a Config object with default parameter.
func DefaultConfig() Config {
	return Config{
		Host:                   "",
		Port:                   2222,
		ServerKeyFilename:      []string{"serverkey.key"},
		MaxNumberOfConnections: 0,
	}
}

// ConfigCmd extends the basic Config with parameters to run an arbitrary cli program over ssh
type ConfigCmd struct {
	Config
	// A list of authorized_keys entry (not the file, the actual keys)
	AuthorizedKeys []string
	// The command to start on an ssh connection
	Command string
	// A list of parameter to give the Command on starting
	CommandArgs []string
}

// ContextCmd is a shared state between all ssh connections on server and the server itself
type ContextCmd struct {
	config *ConfigCmd
	// Number of ssh connection that are currently active
	activeConnections int32
}

// DefaultCmdConfig creates a ConfigCmd instance with default values
func DefaultCmdConfig() ConfigCmd {
	return ConfigCmd{
		Config:         DefaultConfig(),
		AuthorizedKeys: []string{},
		Command:        "cat",
		CommandArgs:    []string{},
	}
}

// LoadConfigCmd parse the ConfigCmd from a toml file with the given filename
func LoadConfigCmd(filename string) (ConfigCmd, error) {
	var c ConfigCmd
	data, err := os.ReadFile(filename)
	if err != nil {
		return c, err
	}
	err = toml.Unmarshal(data, &c)
	return c, err
}

// MakeContextCmd creates a [ContextCmd] from the [ConfigCmd]
func (c *ConfigCmd) MakeContextCmd() ContextCmd {
	return ContextCmd{
		config:            c,
		activeConnections: 0,
	}
}

// Checks whether the given public key is known in the authorized_keys entry from the config.
func (c *ConfigCmd) checkValidKey(key gssh.PublicKey) bool {
	for _, data := range c.AuthorizedKeys {
		allowedKey, _, _, _, _ := ssh.ParseAuthorizedKey([]byte(data))
		if gssh.KeysEqual(key, allowedKey) {
			return true
		}
	}
	return false
}

// Handles a new ssh session by starting the desired application and expose it through this session.
func (c *ContextCmd) handle(s gssh.Session) {
	log.Printf("Connect with %s\n", s.RemoteAddr().String())
	defer log.Printf("Disconnect from %s\n", s.RemoteAddr().String())
	// We do support pty
	ptyReq, winCh, isPty := s.Pty()
	// Check if we have too many connections
	conn := int(atomic.AddInt32(&c.activeConnections, 1))
	defer atomic.AddInt32(&c.activeConnections, -1)
	if c.config.MaxNumberOfConnections > 0 && conn > c.config.MaxNumberOfConnections {
		_, _ = s.Write([]byte("Max Number of Connections reached\n"))
		return
	}
	// We start the command
	cmd := exec.Command(c.config.Command, c.config.CommandArgs...)
	if isPty && WITH_PTY {
		// If we have pty, and we support pty on the platform, we start the pty relevant initialization and the command.
		err := WrapPTY(s, cmd, ptyReq, winCh)
		if err != nil {
			log.Println(err)
			return
		}
	} else {
		// Otherwise we can redirect stdout and copy stdin
		cmd.Stdout = s
		cmd.Stderr = s.Stderr()
		stdin, err := cmd.StdinPipe()
		if err != nil {
			log.Println(err)
			return
		}
		if err := cmd.Start(); err != nil {
			log.Println(err)
			return
		}
		if _, err := io.Copy(stdin, s); err != nil {
			log.Println(err)
			return
		}
		if err := stdin.Close(); err != nil {
			log.Println(err)
			return
		}
		if err := cmd.Wait(); err != nil {
			log.Println(err)
			return
		}
	}
}

func (c *ContextCmd) Listen() {
	publicKeyHandler := func(ctx gssh.Context, key gssh.PublicKey) bool {
		// log.Printf("Connection from %s\n", string(key.Marshal()))
		return c.config.checkValidKey(key)
	}
	s := &gssh.Server{
		Addr:             fmt.Sprintf("%s:%d", c.config.Host, c.config.Port),
		Handler:          c.handle,
		PublicKeyHandler: publicKeyHandler,
	}
	hostkeys, err := c.config.getOrGenerateServerKey()
	fatal(err)
	for _, hostkey := range hostkeys {
		s.AddHostKey(hostkey)
	}
	log.Printf("Listen on %s:%d\n", c.config.Host, c.config.Port)
	fatal(s.ListenAndServe())
}

// If v is a non-nil error, this function prints it and exits the application.
func fatal(v error) {
	if v != nil {
		log.Fatal(v)
	}
}

func mainCmd(args []string) {
	if len(args) != 2 {
		ErrPrintf("Wrong arguments: %s configfile\n", args[0])
		ErrPrintf("\n")
		ErrPrintf("Config file will be created if does not exists\n")
		ErrPrintf("Needed Serverkey will also be created if not exists\n")
		os.Exit(-1)
	}
	if _, err := os.Stat(args[1]); os.IsNotExist(err) {
		c := DefaultCmdConfig()
		file, err := os.OpenFile(args[1], os.O_CREATE|os.O_WRONLY, os.ModePerm)
		fatal(err)
		encoder := toml.NewEncoder(file)
		err = encoder.Encode(&c)
		fatal(err)
		fmt.Printf("Created default config to %s\n", args[1])
		os.Exit(-1)
	}
	c, err := LoadConfigCmd(args[1])
	fatal(err)
	ctx := c.MakeContextCmd()
	ctx.Listen()
}
