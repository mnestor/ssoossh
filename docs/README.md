# SSOoSSH - Single Sign-On over SSH

Small webserver that generates SSH certificates on authorization done by an OIDC server along with a client for retrieval.

Certificates generated should be usable for a short period of time so they work more like tokens to avoid needing to deal with a CRL (Certificate Revocation List). Client is used as an SSH ProxyCommand to retrieve the certificate, load it into the users ssh-agent, then connect to the remote server. Certificate lifetime can be configured in the server configuration though.

## Config

Configuration can be loaded from files and/or environment. All files found will have their settings merged so you can have a global level config and override specific settings as needed per user and per run.

* /etc
* ./
* ~/.config

### Client

Filename: [`ssoossh.yaml`](docs/ssoossh.yaml.default)

At a bare minimum you MUST define `server`

## Server

Server supports hot reloading of most configuration values, only a few settings in the `server` section are not able to be hot reloaded

Filename: [`ssoossh-server.yaml`](docs/ssoossh-server.yaml.default)

## Usage

```
ssh -o ProxyCommand="ssoossh proxycommand %h %p" DestServer
```

Or defined in your `ssh_config`

```
# ~/.ssh/config
Host *
  ProxyCommand ssoossh proxycommand %h %p

# this will now jump through ssoossh getting a cert automatically
$ ssh DestServer
```

If you set the server to issue user certificates with a longer duration you can pre-generate a cert before needing it and avoid using ProxyCommand directive entirely. This is NOT recommended though.

```
# gets a certificate
$ ssoossh login

# view details of the certificate
$ ssoossh inspect

# remove certificate
$ ssoossh logout
```

# SSH Server Configuration

In order to accept clients that are presenting ssh-keys signed by our little app here we need to set a few things in `sshd_config`

You can submit your current ssh key to get signed as well through `curl` calls if you so desire. Though it can be a bit messy!

```
# /etc/ssh/sshd_config
TrustedUserCAKeys /etc/ssh/trusted_cas
```

```
# Now create the file with our ca public key
$ curl -sL https://domain/ca | jq -r '.ca' >> /etc/ssh/trusted_cas
```

# Server API

```
# You MUST configure curl to use cookies in order to access the session created by the first command to then retrieve the certificate!

# Example: your ssh key files are `id_example` and `id_example.pub`
# submit your public key and get back an id
$ curl -sL -c cookies.txt -b cookies.txt -d '{"pubkey": "<contents of id_example.pub>", "type": "user"}' https://domain/api/v1/signreq
# {"status":"success","ID":"004326c3-4bfc-44a8-b3c0-8042f22d4aa9"}

# use the ID value from the response
# open your browser to https://domain/approve/<ID>

# get the certificate with curl
$ curl -sL -c cookies.txt -b cookies.txt https://domain/api/v1/certificate
# {"status":"success","certificate":"ssh-ed25519-cert-v01@openssh.com AAA...."}

# Pipe it into `jq` or just parse it (eww) and save it to `id_example-cert.pub`
# you are now able to ssh with that key, load it into your ssh-agent or use it as files
```
