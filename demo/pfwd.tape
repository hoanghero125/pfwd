# The README's demo: pfwd connects to devbox, lists what listens there,
# and auto-forwards a server that starts a few seconds later.
# Render it with `make demo` after demo/setup.sh (CI's demo workflow does both).
Output demo/pfwd.gif

Require pfwd
Set Shell "bash"
Set FontSize 15
Set Width 1120
Set Height 540
Set Padding 18
Set Theme "Builtin Dark"
Set TypingSpeed 70ms

Hide
Type "export PS1='$ ' && clear"
Enter
# A dev server starts on devbox while pfwd runs.
Type "(sleep 9; demo/start-server.sh 5173 >/dev/null 2>&1) & clear"
Enter
Show

Type "pfwd devbox"
Sleep 500ms
Enter
Sleep 6s
Type "j"
Sleep 700ms
Type "j"
Sleep 700ms
Type "k"
Sleep 7s
Screenshot demo/pfwd.png
Type "?"
Sleep 3s
Escape
Sleep 1s
Type "q"
Sleep 1.2s
Type "y"
Sleep 1s
