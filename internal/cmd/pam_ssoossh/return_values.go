// Created by Mike Nestor <me@mikenestor.org>
package pam_ssoossh

// The user was not authenticated
const PAM_AUTH_ERR = 9

// For some reason the application does not have sufficient credentials to authenticate the user.
const PAM_CRED_INSUFFICIENT = 11

// The modules were not able to access the authentication information. This might be due to a network or hardware failure etc.
const PAM_AUTHINFO_UNAVAIL = 12

// The supplied username is not known to the authentication service
const PAM_USER_UNKNOWN = 13

// One or more of the authentication modules has reached its limit of tries authenticating the user. Do not try again.
const PAM_MAXTRIES = 8

// Success
const PAM_SUCCESS = 0
