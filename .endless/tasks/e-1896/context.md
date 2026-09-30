Every one of those scripts therefore dies in setup() with "ERROR: registering temp project failed" and verifies nothing — they are silently dead, not passing.

Discovered while updating e-1771-verify.sh for E-1880; the E-1880 assertions in it are correct but cannot execute until this lands.
