#!/bin/bash
if [[ "$OSTYPE" == "msys" || "$OSTYPE" == "win32" ]]; then
    echo "⚠️ Warning: Automated cron backups are only supported on Linux/WSL."
    echo "   Skipping backup configuration."
    # skip the cron prompt
else
    # Run the backup prompt here...
fi

echo "========================================"
echo "🚀 Installing RED Engine..."
echo "========================================"

if [ ! -f "docker-compose.yml" ]; then
    echo "[*] Repository not detected in current directory."

    if ! command -v git &> /dev/null; then
        echo "❌ Error: 'git' is not installed. Please install git to continue."
        exit 1
    fi

    echo "[*] Cloning RED Engine repository..."
    git clone https://github.com/RED-Collective/red-engine.git
    if [ $? -ne 0 ]; then
        echo "❌ Error: Failed to clone repository."
        exit 1
    fi

    echo "[*] Navigating into red-engine directory..."
    cd red-engine || exit 1
else
    echo "[*] Running from inside existing repository."
fi

if [ ! -d "./data" ]; then
    echo "[*] Creating ./data directory..."
    mkdir -p ./data
else
    echo "[*] ./data directory already exists."
fi

# FIX: Ensure the data directory is owned by UID 1000 (the 'reduser' inside the container)
# This prevents permission errors without granting world-writable access.
echo "[*] Setting ownership of ./data to UID 1000..."
sudo chown -R 1000:1000 ./data

if [ ! -f "config.json" ]; then
    echo "[*] Generating default config.json..."
    NEW_TOKEN=$(cat /dev/urandom | tr -dc 'a-zA-Z0-9' | fold -w 24 | head -n 1)

    cat <<EOF > config.json
{
  "addr": ":8080",
  "siteName": "RED Engine",
  "dataDir": "data",
  "adminToken": "$NEW_TOKEN",
  "startupSync": []
}
EOF
    echo "[*] Generated secure Admin Token: $NEW_TOKEN"
    echo "⚠️  PLEASE SAVE THIS TOKEN! You will need it to log in to the admin panel."
else
    echo "[*] config.json already exists. Skipping default generation."
fi

if [ ! -f "contributors.json" ]; then
    echo "[*] Generating default contributors.json..."
    echo "[]" > contributors.json
else
    echo "[*] contributors.json already exists."
fi

# Check for root/sudo privileges (Required to modify sysctl)
if [[ $EUID -ne 0 ]]; then
   echo "⚠️  Sudo privileges are required to modify system network settings."
   echo "Please enter your password when prompted."
   exec sudo "$0" "$@"
fi

# 2. Define the configuration file path
# Using the /etc/sysctl.d/ directory is the modern, persistent way to configure kernel parameters
CONF_FILE="/etc/sysctl.d/80-unprivileged-ports.conf"

echo "[*] Setting unprivileged port start to 80..."

# 3. Write the rule to the persistent boot file
cat <<EOF > "$CONF_FILE"
# Project R.E.D. - Allow rootless Podman to bind to standard web ports
net.ipv4.ip_unprivileged_port_start=80
EOF

# 4. Reload all system configuration files so it takes effect immediately without a reboot
echo "[*] Applying changes immediately..."
sysctl --system > /dev/null

echo "========================================"
echo "✅ Success! Ports 80 and above can now be bound by standard users."
echo "✅ This setting is now permanently baked into your boot configuration."
echo "========================================"

if command -v podman-compose &> /dev/null; then
    COMPOSE_CMD="podman-compose up --build -d"
elif command -v docker-compose &> /dev/null; then
    COMPOSE_CMD="docker-compose up --build -d"
elif command -v docker &> /dev/null && docker compose version &> /dev/null; then
    COMPOSE_CMD="docker compose up --build -d"
else
    echo "❌ Error: Neither podman-compose nor docker compose found on this system."
    echo "Please install Podman or Docker to continue."
    exit 1
fi

echo "[*] Starting RED Engine using container engine..."
$COMPOSE_CMD

if [ $? -ne 0 ]; then
    echo "❌ Error: Failed to start containers."
    exit 1
fi

CONFIG_PORT=$(grep '"addr"' config.json | sed -E 's/.*:([0-9]+).*/\1/')

if [ -z "$CONFIG_PORT" ]; then
    CONFIG_PORT="8080"
fi

HOST_IP="localhost"
if command -v hostname &> /dev/null; then
    HOST_IP=$(hostname -I | awk '{print $1}')
    [ -z "$HOST_IP" ] && HOST_IP="localhost"
fi

echo "========================================"
echo "✅ Installation Complete!"
echo "🌐 Your node is running at: http://${HOST_IP}:${CONFIG_PORT}"
echo "⚙️  Admin Panel: http://${HOST_IP}:${CONFIG_PORT}/-/admin"
echo "========================================"

# --- Automated Backup Setup ---
echo ""
read -p "Would you like to set up daily automatic backups? [Y/n] " response
case "$response" in
    [yY][eE][sS]|[yY]|"") 
        # Define absolute path to this project directory
        PROJECT_PATH=$(pwd)
        CRON_JOB="0 3 * * * $PROJECT_PATH/backup-data.sh"
        
        # Check if the cron job already exists to avoid duplicates
        if crontab -l | grep -q "$PROJECT_PATH/backup-data.sh"; then
            echo "[*] Backup cron job already exists."
        else
            (crontab -l 2>/dev/null; echo "$CRON_JOB") | crontab -
            echo "[*] Backup cron job installed (Runs daily at 03:00)."
        fi
        ;;
    *)
        echo "[*] Backups skipped."
        ;;
esac