# Enhancements

Small feature modifications logged for later. Each entry should say what
changes and why; promote an entry to its own proposal doc if it grows.

## Service account ownership transfer: authorized-user dropdown

Replace the free-form transfer target with a dropdown listing only the
other users already authorized for that service account.

If no other authorized users exist, present an error/warning explaining
the transfer cannot happen yet, and instruct the current owner to have
any known users of the account log in to ssoossh first so they become
selectable transfer targets.
