tasks.analysis is a real DB column with a documented purpose (research/exploration content, per E-1073), but neither `endless task add` nor `endless task update` exposes a flag to set it.

Agents and humans drop down to `endless sql --write` to populate the field (hit during E-1325 filing).
