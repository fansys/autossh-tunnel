#!/bin/sh

# Load PUID and PGID from environment variables
PUID=${PUID:-1000}
PGID=${PGID:-1000}

# Modify the existing user and group to match PUID and PGID
if [ "$(id -u myuser)" != "$PUID" ] || [ "$(id -g myuser)" != "$PGID" ]; then
	sed -i "s/^myuser:x:[0-9]*:[0-9]*:/myuser:x:$PUID:$PGID:/" /etc/passwd
	sed -i "s/^mygroup:x:[0-9]*:/mygroup:x:$PGID:/" /etc/group
fi

# Ensure log and temporary directories exist with proper ownership
mkdir -p /tmp/autossh-logs /home/myuser/.ssh
chmod 777 /tmp/autossh-logs
chown -R myuser:mygroup /tmp/autossh-logs /home/myuser

# Ensure config directory has proper ownership
if [ -d /etc/autossh/config ]; then
	chown -R myuser:mygroup /etc/autossh/config
fi

# Switch to myuser and execute the server binary
exec su-exec myuser "$@"
