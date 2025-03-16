sudo dpkg --add-architecture x86-64
sudo dpkg --add-architecture amd64
sudo apt update
sudo apt install -y libpam0g-dev:amd64 libc6-dev-amd64-cross libpam0g-dev:arm64 libc6-dev-arm64-cross libgcc-12-dev-arm64-cross libgcc-12-dev-amd64-cross gcc-x86-64-linux-gnu
