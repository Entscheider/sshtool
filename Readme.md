# Introduction

SSHTool is a go application that implements some ssh capabilities in a simple-to-use way. In detail:

1. Generating ed25519 private/public keys.
2. Exposing a terminal program via an ssh connection. Including pty support (on unix).
3. Exposing one or more directories via **sftp**. Using regular expressions, you can set read and write permissions
   separately from the operating system as well as hide files from public view.
   For application/operating systems which doesn't support sftp connection, SSHTool additionally can start a **webdav**
   server which can be forwarded with ssh to localhost in order to connect to it.

Note that SSHTool only supports public key authentication.

# Usage

## Key generation

To generate an ed25519 key pair execute

```bash
sshtool generate sshkey
```

It creates the private file under `sshkey` and the corresponding public key named `sshkey.pub`.

Be aware that you don't need to manually generate the key for SSHTool's remaining commands.
Private keys that were not found will be generated there automatically.

## Program exposing

For exposing another program in an ssh session, execute

```bash 
sshtool cmd config.toml
```

where `config.toml` is the configuration file containing the details.
A standard configuration file is created if the given filename does not exist.
In the config file you can set the hostname the ssh server is listen to as well as the port.

### Connecting

For connecting to the ssh server, execute

```bash
ssh username@servername -p 2222
```

where `username` can be arbitrary, `servername` is the ip address or dns name of the host and 2222 is the port set in
the configuration.

### Configuration details

The `ServerKeyFilename` is an array with different private keys the server offers the client and will sign requests with.
Every key must use a different signature scheme (rsa, ed25519, etc.).
If a key is not found, a random ed25519 key is generated and saved at the location automatically.

The parameter `MaxNumberOfConnections` is the maximal number of parallel ssh connection accepted by the server.
A value of 0 means no limit.

`AuthorizedKeys` is a list of public ssh keys accepted from a client.
The format of every entry is the same as in the `authorized_keys` file ssh expects.
E.g. "ssh-ed25519 AAAAXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX someone@somewhere".

`Command` is the path of the application to execute and `CommandArgs` a list of arguments to start it with.

## SFTP

For starting the sftp server, call

```bash 
sshtool sftp config.toml
```

Note that the `config.toml` is different from the cmd subcommand.
If the config file does not exist, it will be created automatically.

### Connecting

Connecting to the sftp server depends on the client you choose.
For Linux there is [sshfs](https://github.com/libfuse/sshfs) and [oxfs](https://github.com/oxfs/oxfs). So you can run

```bash 
sshfs username@servername:/ -p 2222 mount
```

to mount the sftp filesystem under "mount". Here 2222 is the port configured, `servername` is ip address or dns name
of the server this app runs on and `username` is the user you want to connect as.
All accepted users along with the accepted authorized keys are listened in the configuration file.
Every user can be served a different filesystem with different read/write permissions.

This filesystem can also be served as webdav if set in the config. This is done by forwarding the webdav
http port to localhost.
If the server runs in sftp mode, there is no stdin/stdout supported. So you have to forward it with

```bash
ssh -NT -L 8080:localhost:80 username@servername -p 2222
```

where 80 is the port described in the config and 8080 is the port opened on the client a webdav client
can connect to. The webdav server has no login requirement.

### Configuration

Most settings match the one from program exposing. In addition to that we have

* `WebDavPort` which is the port the webdav server listen to and can be forwarded from. Note that
  the server listen on a virtual port and not on an actual port on the operating system. Thus, the only
  way to connect to it is by ssh tcp/ip forwarding.
* `Users` is a list of allowed usernames along with configuration for this user. In the default config
  a standard user with the name `user` is created. You need to rename this phrase for change the name.
* `CanRead` is a list of regular expression for files that can be read from a client.
* `CanWrite` is a list of regular expression for files that can be written from a client.
* `ShouldHide` is a list of regular expression for files that are hidden from a client.
* `WebDav` enables the webdav server that can be forwarded with ssh if true. It exposes the exact same filesystem sftp does.
* `FileSystem` lists all directories that can be accessed from a client. If a directory has the name "" in the config,
  it will be exposed directly. If not, a client sees a virtual filesystem containing every directory with the name in
  this config.
* `Root` is the path of the directory to expose.
* `ReadOnly` sets that this directory can only be read and not be written. It has some overlaps with the `CanRead` config.

# Building

As SSHTool is written in golang, simple run

```bash
go build -o sshtool
```

for compilation. By setting the "GOOS" and "GOARCH" environment variable, cross compiling is possible.

# License

The code is distributed under AGPL-3.0.