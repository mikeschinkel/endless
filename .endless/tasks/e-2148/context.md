'show' lists rather than showing one item, and there is no detail view for a single error even though the listing truncates summaries.

The default listing omits the project, so 'errors show' printed 'no errors' inside one project while the status line reported one error and one warning from another -- two surfaces flatly disagreeing.

The listing wraps and spends ten characters spelling out severities that an icon conveys, in both the list and the status line.

ERR-0001 carries an ERR prefix while being severity warning.

Nothing anywhere tells a user how to resolve anything, though docs/errors.md already carries a remedy per code.

And the one-line notification those status views append is called a 'badge' throughout -- a name a session coined and later sessions treated as settled vocabulary; Mike finds it unintuitive and wants 'notification row'.
