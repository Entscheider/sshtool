package main // import "github.com/Entscheider/sshtool"

import "os"
import "fmt"
import "io"

// A subcommand we support.
type cmd struct {
	// The function for handling this command. It will be called with the program arguments for this
	// application skipping the program name and the command name.
	f func([]string)
	// A string describing the command
	help string
}

var CMDS = map[string]cmd{
	"cmd":      {mainCmd, sshcmdhelp},
	"sftp":     {mainSftp, sftpHelp},
	"generate": {main_sshgen, sshgenhelp},
}

// Prints all available commands to the given writer
func printcmds(writer io.Writer) {
	for name, val := range CMDS {
		_, _ = fmt.Fprintf(writer, "%s - %s\n", name, val.help)
	}
}

// ErrPrintf is a convenient function to print the formatted string to [os.Stderr]
func ErrPrintf(s string, args ...interface{}) {
	f := os.Stderr
	_, _ = fmt.Fprintf(f, s, args...)
}

func main() {
	args := os.Args
	if len(args) < 2 {
		ErrPrintf("Missing command: %s commandname args\n", args[0])
		ErrPrintf("\n%s\n", "Where commandname is one of the follow:")
		printcmds(os.Stderr)
		return
	}
	cmd, ok := CMDS[args[1]]
	if !ok {
		ErrPrintf("Command %s not found\n", args[1])
		ErrPrintf("Available Commands: \n")
		printcmds(os.Stderr)
		return
	}
	newargs := []string{fmt.Sprintf("%s %s", args[0], args[1])}
	newargs = append(newargs, args[2:]...)
	cmd.f(newargs)
}
