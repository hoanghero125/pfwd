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
Set Theme { "name": "pfwd", "background": "#131416", "foreground": "#E9E9EC", "selection": "#2A2C31", "cursor": "#6CC4D9", "black": "#18191C", "red": "#EC6F67", "green": "#7DC88E", "yellow": "#E2B45A", "blue": "#6CA6D9", "magenta": "#C49BE0", "cyan": "#6CC4D9", "white": "#E9E9EC", "brightBlack": "#8F929A", "brightRed": "#F28B84", "brightGreen": "#98D6A6", "brightYellow": "#EAC57E", "brightBlue": "#8DBAE3", "brightMagenta": "#D3B3EA", "brightCyan": "#8FD3E3", "brightWhite": "#FFFFFF" }
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
