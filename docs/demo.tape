# Records docs/demo.gif. Needs a fresh stack so the revisions start at 1:
#   docker compose -p relay-demo --profile server up -d --build --wait
#   vhs docs/demo.tape
#   docker compose -p relay-demo --profile server down -v

Output docs/demo.gif

Set Shell bash
Set FontSize 16
Set Width 1200
Set Height 460
Set Padding 24
Set TypingSpeed 15ms
Set Theme "Catppuccin Mocha"

Hide
Type "export PS1='$ ' && clear"
Enter
Show

Type "# alice is in finance, finance can view its folder, and the budget is in that folder"
Enter
Sleep 500ms
Type `curl -s -X POST localhost:8080/v1/tuples -d '{"tuple": "group:finance#member@user:alice"}'`
Enter
Sleep 400ms
Type `curl -s -X POST localhost:8080/v1/tuples -d '{"tuple": "folder:finance#viewer@group:finance#member"}'`
Enter
Sleep 400ms
Type `curl -s -X POST localhost:8080/v1/tuples -d '{"tuple": "document:budget#parent@folder:finance"}'`
Enter
Sleep 1s

Type "# can alice view the budget?"
Enter
Type `curl -s 'localhost:8080/v1/check?object=document:budget&relation=viewer&user=user:alice'`
Enter
Sleep 1.5s

Type "# what can alice view?"
Enter
Type `curl -s 'localhost:8080/v1/lookup?type=document&relation=viewer&user=user:alice'`
Enter
Sleep 1.5s

Type "# remove alice from finance, then check again at that revision or newer"
Enter
Type `curl -s -X DELETE localhost:8080/v1/tuples -d '{"tuple": "group:finance#member@user:alice"}'`
Enter
Sleep 800ms
Type `curl -s 'localhost:8080/v1/check?object=document:budget&relation=viewer&user=user:alice&at_least=4'`
Enter
Sleep 4s
