package main

import (
	"context"
	"fmt"
	"github.com/BurntSushi/toml"
	"github.com/Entscheider/sshtool/logger"
	mware "github.com/Entscheider/sshtool/middleware"
	sftp2 "github.com/Entscheider/sshtool/sftp"
	"github.com/Entscheider/sshtool/sshport"
	"github.com/Entscheider/sshtool/webdav_fs"
	gosftp "github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"log"
	"net"
	"net/http"
	"os"
	"regexp"

	gssh "github.com/gliderlabs/ssh"
)

// See also https://github.com/pkg/sftp/blob/master/examples/sftp-server/main.go

const sftpHelp = "Expose directories through sftp with multiple user support"

// ConfigSftp describes a parsed yaml config containing all parameters for starting the sftp server.
type ConfigSftp struct {
	Config
	// The users we accept along with further config for this user.
	Users map[string]UserEntry
	// The port a webdav server can be forwarded from
	WebDavPort uint32
}

// UserEntry contains the setting of the sftp connection for a particular user.
type UserEntry struct {
	// A list of authorized keys we accept for a connection from this user.
	// This list contains the actual public keys (not the filename) formatted
	// in the same way the "authorized_keys" lines are formatted.
	AuthorizedKeys []string
	// The directories we serve to the user. All are listed under a specific name in a virtual root directory.
	// If a directory is listed under an empty key "", this directory is served only without a virtual root filesystem.
	Filesystem map[string]SFTPEntry
	// List of strings containing regular expression for files that can be read. E.g. ".*" to allow all files to be read.
	// This regular expression are matched against the path relative to (virtual) root directory served to the user.
	CanRead []string
	// List of strings containing regular expression for files that can be written. E.g. ".*" to allow all files to be read.
	// This regular expression are matched against the path relative to (virtual) root directory served to the user.
	CanWrite []string
	// List of strings containing regular expression for files should be hidden.
	// This regular expression are matched against the path relative to (virtual) root directory served to the user.
	ShouldHide []string
	// Whether to enable webdav for this user
	WebDav bool
}

// SFTPEntry contains information about a served directory
type SFTPEntry struct {
	// The root path which contents should be served
	Root string
	// Whether to serve this directory without any writing-permissions. Has some overlaps with CanWrite (see above).
	ReadOnly bool
}

// DefaultSftpConfig creates a ConfigSftp with some default parameters.
func DefaultSftpConfig() ConfigSftp {
	return ConfigSftp{
		Config:     DefaultConfig(),
		WebDavPort: 80,
		Users: map[string]UserEntry{
			"user": {
				AuthorizedKeys: []string{"ssh-key AAANCC someone@somehwere"},
				Filesystem: map[string]SFTPEntry{
					"": {Root: "/", ReadOnly: true},
				},
				CanRead:    []string{".*"},
				CanWrite:   []string{".*"},
				ShouldHide: []string{},
				WebDav:     false,
			},
		},
	}
}

// Returns a function that checks if a public key from a user matches one authorized key from the config
// for this user.
func (c *ConfigSftp) buildKeyValidationFunc() (func(username string, key gssh.PublicKey) bool, error) {
	// We build a map in which we enumerate all parsed public keys accepted for a particular user.
	keysPerUser := make(map[string][]ssh.PublicKey)
	for username, entry := range c.Users {
		keys := make([]ssh.PublicKey, len(entry.AuthorizedKeys))
		for i, keyString := range entry.AuthorizedKeys {
			allowed, _, _, _, err := ssh.ParseAuthorizedKey([]byte(keyString))
			if err != nil {
				return nil, err
			}
			keys[i] = allowed
		}
		keysPerUser[username] = keys
	}
	// The validation function than simple checks if the public key from the user matches any of the allowed one.
	return func(username string, key gssh.PublicKey) bool {
		if keys, ok := keysPerUser[username]; ok {
			for _, publicKey := range keys {
				if gssh.KeysEqual(publicKey, key) {
					return true
				}
			}
		}
		return false
	}, nil
}

// ContextSftp contains some information for a serving sftp server.
type ContextSftp struct {
	// The config this app has.
	config *ConfigSftp
	// TCP/IP forwarding helper object
	tcpipHandler sshport.SSHConnectionHandler
	// The number of currently active connections.
	// TODO: This doesn't get increased anywhere
	activeConnections int32
	// Object to log all access and logins.
	accessLogger logger.AccessLogger
	// Object to log debug and errors.
	logger logger.Logger
}

// Creates a (possible virtual) root [sftp2.SimplifiedFS] from a UserEntry, which describes the directories
// to serve along with access information.
// The returning fs has no permission check yet. So it usually needs to be wrapped into a [sftp2.PermWrapperFS]
func (c *ConfigSftp) createFSWithoutPermission(userEntry UserEntry) sftp2.SimplifiedFS {
	if entry, ok := userEntry.Filesystem[""]; ok {
		// We serve only one fs at the top
		return sftp2.DirFs{Root: entry.Root, Readonly: entry.ReadOnly}
	}
	// We must create a virtual fs that servers every directory
	fsMap := make(map[string]sftp2.SimplifiedFS)
	for path, entry := range userEntry.Filesystem {
		fsMap[path] = sftp2.DirFs{
			Root: entry.Root, Readonly: entry.ReadOnly,
		}
	}
	return sftp2.CombinedFS{Dirs: fsMap}
}

// Converts an array of strings into a parsed array of regular expressions.
func intoRegexp(array []string) ([]*regexp.Regexp, error) {
	res := make([]*regexp.Regexp, len(array))
	for i, exp := range array {
		rexp, err := regexp.Compile(exp)
		if err != nil {
			return nil, err
		}
		res[i] = rexp
	}
	return res, nil
}

// CreateFS creates a (possible virtual) root [sftp2.SimplifiedFS] from a UserEntry, which describes the directories
// to serve along with access information. The returning fs also checks the required access permissions for a file.
func (c *ConfigSftp) CreateFS(username string) (sftp2.SimplifiedFS, error) {
	userEntry, ok := c.Users[username]
	if !ok {
		return nil, fmt.Errorf("user %s has no config entry", username)
	}
	fs := c.createFSWithoutPermission(userEntry)
	if len(userEntry.ShouldHide) == 0 && len(userEntry.CanRead) == 0 && len(userEntry.CanWrite) == 0 {
		return fs, nil
	}
	canReadRegexp, err := intoRegexp(userEntry.CanRead)
	if err != nil {
		return nil, err
	}
	canWriteRegexp, err := intoRegexp(userEntry.CanWrite)
	if err != nil {
		return nil, err
	}
	shouldHideRegexp, err := intoRegexp(userEntry.ShouldHide)
	if err != nil {
		return nil, err
	}
	return sftp2.PermWrapperFS{
		Inner:            fs,
		CanReadRegexp:    canReadRegexp,
		CanWriteRegexp:   canWriteRegexp,
		ShouldHideRegexp: shouldHideRegexp,
	}, nil
}

// LoadConfigSftp loads and parses a toml file that contains the configuration for creating a sftp server.
func LoadConfigSftp(filename string) (ConfigSftp, error) {
	var c ConfigSftp
	data, err := os.ReadFile(filename)
	if err != nil {
		return c, err
	}
	//err = json.Unmarshal(data, &c)
	err = toml.Unmarshal(data, &c)
	return c, err
}

// MakeContext converts a [ConfigSftp] into [ContextSftp] by adding default values.
func (c *ConfigSftp) MakeContext() ContextSftp {
	log := logger.NewLogger(os.Stdout)
	return ContextSftp{
		config:            c,
		activeConnections: 0,
		accessLogger:      logger.NewAccessLogger(os.Stdout),
		logger:            log,
		tcpipHandler:      sshport.NewSSHConnectionHandler(log, context.Background()),
	}
}

// Listen starts the sftp server.
func (c *ContextSftp) Listen(ctx context.Context) {
	// Build a function that validates ssh connection request and rejects them if they are not authorized.
	validationF, err := c.config.buildKeyValidationFunc()
	fatal(err)
	// validationF -> public key validation function expected from the ssh package.
	publicKeyHandler := func(ctx gssh.Context, key gssh.PublicKey) bool {
		username := ctx.User()
		//fmt.Printf(string(gossh.MarshalAuthorizedKey(key)))
		return validationF(username, key)
	}
	// This function creates the [sftp.Handlers] filesystem for the user of the connection.
	sftpHandler := func(connectionInfo logger.ConnectionInfo) gosftp.Handlers {
		fs, err := c.config.CreateFS(connectionInfo.Username)
		if err != nil {
			// On error, we serve an empty fs
			c.logger.Err("ContextSftp", fmt.Sprintf("Error while creating virtual fs for user %s: %s", connectionInfo.Username, err.Error()))
			fs = sftp2.EmptyFS{}
		}
		return sftp2.CreateSFTPHandler(fs, c.accessLogger, connectionInfo, c.logger)
	}
	s := &gssh.Server{
		Addr: fmt.Sprintf("%s:%d", c.config.Host, c.config.Port),
		Handler: func(s gssh.Session) {
			// We do not allow non-sftp connections
			c.logger.Info("ContextSftp", fmt.Sprintf("Denying non-sftp access to %s at %s", s.User(), s.RemoteAddr()))
			_, _ = s.Write([]byte("Not allowed"))
		},
		SubsystemHandlers: mware.AddSftpSubsystemHandler(sftpHandler, c.accessLogger, gssh.DefaultSubsystemHandlers),
		PublicKeyHandler:  publicKeyHandler,
		ConnectionFailedCallback: func(conn net.Conn, err error) {
			c.logger.Info("SFTPServer", fmt.Sprintf("Connection failed for %s: %v", conn.RemoteAddr().String(), err))
		},
		LocalPortForwardingCallback: func(ctx gssh.Context, destinationHost string, destinationPort uint32) bool {
			// We allow port forwarding if webdav is enabled
			userConfig, ok := c.config.Users[ctx.User()]
			if !ok || !userConfig.WebDav {
				return false
			}
			return true
		},
	}
	// Add the tcp/ip forward handler to the connection
	s.ChannelHandlers = map[string]gssh.ChannelHandler{
		"session":      gssh.DefaultSessionHandler,
		"direct-tcpip": c.tcpipHandler.HandleTCPIP,
	}
	// We generate private and public keys if they don't exist yet.
	hostkeys, err := c.config.getOrGenerateServerKey()
	fatal(err)
	for _, hostkey := range hostkeys {
		s.AddHostKey(hostkey)
	}
	log.Printf("Listen on %s:%d\n", c.config.Host, c.config.Port)
	// Start the webdav server on the virtual tcp/ip connections
	c.startTcpip(ctx)
	fatal(s.ListenAndServe())
}

// startTcpip starts for every user a webdav server (if desired) that listens
// on the tcp/ip forwarded ssh connection.
func (c *ContextSftp) startTcpip(ctx context.Context) {
	for username, entry := range c.config.Users {
		if !entry.WebDav {
			continue
		}
		// Create a new net.Handler that works over ssh and serve a webdav http server over it.
		listener := c.tcpipHandler.CreateListener(c.config.WebDavPort, username)
		fs, err := c.config.CreateFS(username)
		if err != nil {
			c.logger.Err("startTcpip", fmt.Sprintf("Cannot create fs for user %s: %v", username, err))
			continue
		}
		webdavHandler := webdav_fs.CreateHandlerForFS(fs, c.logger)
		server := http.Server{
			Handler:     webdavHandler,
			BaseContext: func(l net.Listener) context.Context { return ctx },
		}
		// When the context say to cancel, we close the server
		go func() {
			<-ctx.Done()
			err := server.Close()
			if err != nil {
				c.logger.Err("startTcpip", err.Error())
			}
		}()
		// We start the server in a separate go routine
		go func() {
			err := server.Serve(listener)
			if err != nil {
				c.logger.Err("startTcpip", err.Error())
			}
		}()
	}

}

// Starts the sftp server
func mainSftp(args []string) {
	if len(args) != 2 {
		ErrPrintf("Wrong arguments: %s configfile\n", args[0])
		ErrPrintf("\n")
		ErrPrintf("Config file will be created if does not exists\n")
		ErrPrintf("Needed Serverkey will also be created if not exists\n")
		return
	}
	if _, err := os.Stat(args[1]); os.IsNotExist(err) {
		{
			c := DefaultSftpConfig()
			file, err := os.OpenFile(args[1], os.O_CREATE|os.O_WRONLY, os.ModePerm)
			fatal(err)
			defer func(file *os.File) {
				err := file.Close()
				if err != nil {
					ErrPrintf("Error while closing config file: %v", err)
				}
			}(file)
			encoder := toml.NewEncoder(file)
			err = encoder.Encode(&c)
			fatal(err)
			fmt.Printf("Created default config to %s\n", args[1])
		}
		os.Exit(-1)
	}
	c, err := LoadConfigSftp(args[1])
	fatal(err)
	ctx := c.MakeContext()
	ctx.Listen(context.Background())
}
