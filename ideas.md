* the only way we can do a krl on service accounts is if ldap is configured. a config for how often to update user account status and then a cronjob that checks all users in our db for account disabled. locked should be short term so not worth a revocation.
* krl's they are really only good for host certs, those are truely the only long lived certs we issue and clients don't do krl lookups. and a krl list for 8-10 hour certs is a low priority.
* we'll have to add ldap to the end of the plans then with config on getting extra user data. not sure what beyond groups, shared accounts, service accounts, and disabled status.
* default cookie_max_age of 0 is bad, need at least a few minutes because 0 just invalidates right away and login fails
* auditor model, let them have a read only view of all certs issued
* we have to hook up some codegen chain to the build process for tygo, does it need openapi too?
* we can do a docker plan for e2e, github runners allow for docker containers and we need to use them anyway when we setup the build process for pam in order to link against old glibc
* this devcontainer is now running on a linux amd64 host. we aren't blocked on pam now. though we do need to hook up docker outside of docker so that within the devcontainer we can run our own containers of old linux to build pam against older glibc versions
* ui/ux take insirpation from pocket-id, again. i like the MyApps for listing out hosts a user enrolled. audit log is another good one too.
* did we resolve the security finding for csrf?

## Admin model plan

### Who needs it responses

Certificate history across all users - auditor role, can see it all
Effective configuration view - since it can't change this is auditor as well
Editing source-network-policy - for lowering lifetimes this is based for a host. the "admin" in that case is the one that created a host cert for that server. we tie the policy to a host cert issuance
Manually expiring an enrollment - this is the one item that truely is an admin function. this needs to get implemented and tied with revocation i describe next
Revocation, disabling a departed user - 2 ways to do this. 1. based on account disabled from ldap. requires ldap config. 2. admin can disable a user and a grace period (defined in config) before certs approved by that user have their service keys expired, we don't really care about user certs for this. should show which certs will be disabled when disabling a user. if a disabled user tries to authenticate again they can't get in. but, we don't issue long lived service certs, just keys to get a service cert. the only long lived certs are host which clients won't deal with a krl

## keyid template

* approver client-ip from headers thanks to trusted proxy should be an option to add to the keyid for all cert types

## certificate lifetime policy

* source-address will only ever be used on service certificates. i see no functional value in them for users where other options exist for lowering cert lifetime
* service certs should be have configurable locked issuance. at approval time the approver can set a subnet to allow retrieval from. if the retrieve happens outside that subnet maybe we expire the retrieval key? that should be a config option, maybe even editable by approver
* if we did keep scoring i'd like to log along side the approval record or linked to it how the score was calculated
* being able to approve a service cert should also be conditional on them having a service account, which should be there from oidc or ldap enrichment. requiring group membership on these shouldn't be considered
* on the note about the schema gap. client requests a service enrollment, browser approving specifies which account it's for (as we discussed recently). that closes that gap
* i can see scoring being drawn 2 different ways on this. trusted subnet for 192.168.10.0/24 then we have a host rule that says lower the duration of any keys issued from me (client running on that host, rule done with host cert enrollment) and the host is 192.168.10.9. the host rule wins completely here, not even the trusted subnet can bring the lifetime up

## misc

* we need to allow users to set backup owners of host certs. this would cover the gave when a user gets disabled, we could notify the backups and give them the option to take ownership then the host renewals wouldn't need updating

## defered till first release is done

* multi-instance
* signer split
* new feature idea: print qr code to terminal for phone login
