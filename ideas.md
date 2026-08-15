* pam can use authorized principals map
* hook up acme for http tls
* there has got to be a better and more tested way to implement argument parsing in pam_ssoossh
* break up devcontainer into usable pieces. so someone can pull the repo and start working on the server without needing everything pam related? good idea? simplest devcontainer needed to work on just client and server
* fix host:~/.local bind mounting as root:root in devcontainer
* auto-start codebase-memory-mcp in contiainer
* ensure rtk and codebase-memory-mcp persist their settings/database
* sort client config. the locked settings should enforce fips mode while client should just warn. split that setting
* review config and hook up with spf13/cobra the flags we use. ensure we have flags for everything that should be there. key settings/size i think should be flag overridable

* after every container rebuild i need to:
  fix ~/.local permissions back to vscode:vscode
  open a terminal and start codebase-memory-mcp
  index_repository for codebase-memory-mcp
  tell me the solution to fix the permission issue and how to save the data from rtk and codebase-memory-mcp so it persists though rebuilds

* at work we have a numeric field in ldap that we can get over oidc. the higher this value the more trusted the individual. there is a case that anyone below a certain threshold can't have sudo rights though. i want to add the ability to dynamically set this so a config value template that can accept <, >, ==, >=, etc. and type cast would work well. if nothing no comparisson is in that template it'll default to ==
