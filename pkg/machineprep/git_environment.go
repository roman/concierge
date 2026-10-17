package machineprep

// Git's SSH child retains repository authentication. Its environment excludes
// model credentials.
// The fixed SetEnv value also replaces inline values from the SSH config.
const repositorySSHCommand = `env -i PATH="$PATH" HOME="$HOME" USER="${USER:-}" LOGNAME="${LOGNAME:-}" SSH_AUTH_SOCK="${SSH_AUTH_SOCK:-}" ssh -S none -oBatchMode=yes -oStrictHostKeyChecking=yes -oUpdateHostKeys=no -oForwardAgent=no -oForwardX11=no -oForwardX11Trusted=no -oClearAllForwardings=yes -oSetEnv=CONCIERGE_SSH_TRANSPORT=1`
