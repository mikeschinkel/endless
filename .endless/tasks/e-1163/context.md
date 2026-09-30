esf (E-1159) clears only the env vars that 'endless session use' is documented to export — currently just ENDLESS_SESSION_ID per E-1038. A user-authored .endless/extensions/use.sh (E-1014) can export arbitrary additional vars (e.g., PATH alterations, NVM_DIR, project-specific exports).

esf has no way to know what an extension set, so those leak.
