# sockpty

Atempt to replicate `flatpak-spawn --host` functionality (within toolbx) using unix domain sockets while learning golang.

- [x] host command exit codes
- [x] sudo without having to specify `-S`
- [x] terminal rezising
- [x] execute commands with env vars from client
- [ ] piping into stdin of host command from client

Original code from : https://github.com/StalkR/misc/tree/master/pty
