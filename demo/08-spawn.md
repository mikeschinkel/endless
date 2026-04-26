# 8. Spawn — tmux send-keys


``` shell
# 6. Spawn without plan mode
endless task spawn  E-776 \
    --no-plan
```

## Setup 
``` shell
# 1. Register project
endless register ~/Projects/happy-face \
  --name happy-face \
  --infer
``` 

``` shell
# 2. Add the task
endless task add "Build happy face website" \
  --project happy-face 
``` 

``` shell
# 3. Check what ID it got
endless task list \
    --project happy-face \
    --sort id    # This should be the default(!)
``` 

``` shell
# 4. Show the prompt
cat ~/Projects/happy-face/prompt.md
``` 

``` shell
# 5. Attach the prompt
endless task update E-776 \
    --prompt ~/Projects/happy-face/prompt.md
``` 
