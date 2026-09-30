E-2071 gave nine task/decision/epic surfaces a shared row cap that announces what it dropped (src/endless/rowcap.py, '… N more rows (--no-limit)'). Its scope deliberately stopped short of the session listings, which still carry the original defect:

`session list`, `session search` and `session history` each cap at a silent --limit 20 and `session trail` at 50, with no footer — a result of exactly 20 is indistinguishable from a truncated one, which is the failure mode E-2071 was filed against.
