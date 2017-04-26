package dog

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/moooofly/dog/run"
)

// ErrCycleInTaskChain means that there is a loop in the path of tasks execution.
var ErrCycleInTaskChain = errors.New("TaskChain includes a cycle of tasks")

// TaskChain contains one or more tasks to be executed in order.
type TaskChain struct {
	Tasks []Task
}

// NewTaskChain creates the task chain for a specific dogfile and task.
func NewTaskChain(d Dogfile, task string) (taskChain TaskChain, err error) {
	err = taskChain.generate(d, task)
	if err != nil {
		return
	}
	return
}

// Generate recursively iterates over all tasks, including pre and post tasks for
// each of them, and adds all of them into a task chain.
func (taskChain *TaskChain) generate(d Dogfile, task string) error {

	t, found := d.Tasks[task]
	if !found {
		return fmt.Errorf("Task %q does not exist", task)
	}

	// Cycle detection
	for i := 0; i < len(taskChain.Tasks); i++ {
		if taskChain.Tasks[i].Name == task {
			if len(taskChain.Tasks[i].Pre) > 0 || len(taskChain.Tasks[i].Post) > 0 {
				return ErrCycleInTaskChain
			}
		}
	}

	// Iterate over pre-tasks
	if err := addToChain(taskChain, d, t.Pre); err != nil {
		return err
	}

	// Add current task to chain
	taskChain.Tasks = append(taskChain.Tasks, *t)

	// Iterate over post-tasks
	if err := addToChain(taskChain, d, t.Post); err != nil {
		return err
	}
	return nil
}

// addToChain adds found tasks into the task chain.
func addToChain(taskChain *TaskChain, d Dogfile, tasks []string) error {
	for _, name := range tasks {

		t, found := d.Tasks[name]
		if !found {
			return fmt.Errorf("Task %q does not exist", name)
		}

		if err := taskChain.generate(d, t.Name); err != nil {
			return err
		}
	}
	return nil
}

// Run handles the execution of all tasks in the TaskChain.
func (taskChain *TaskChain) Run(stdout, stderr io.Writer) error {
	var startTime time.Time
	var registers []string

	for _, t := range taskChain.Tasks {
		var err error
		var runner run.Runner
		register := new(bytes.Buffer)

		exitStatus := 0
        // 前一个 task 中的 register 值作为了后一个 task 的 env 使用
		env := append(t.Env, registers...)

		switch t.Runner {
		case "sh":
			runner, err = run.NewShRunner(t.Code, t.Workdir, env)
		case "bash":
			runner, err = run.NewBashRunner(t.Code, t.Workdir, env)
		case "python":
			runner, err = run.NewPythonRunner(t.Code, t.Workdir, env)
		case "ruby":
			runner, err = run.NewRubyRunner(t.Code, t.Workdir, env)
		case "perl":
			runner, err = run.NewPerlRunner(t.Code, t.Workdir, env)
		case "nodejs":
			runner, err = run.NewNodejsRunner(t.Code, t.Workdir, env)
		case "go":
			runner, err = run.NewGoRunner(t.Code, t.Workdir, env)
		default:
			if t.Runner == "" {
				return errors.New("Runner not specified")
			}
			return fmt.Errorf("%s is not a supported runner", t.Runner)
		}
		if err != nil {
			return err
		}

		// runner => 即 &runCmd{} 对应底层 exec.Cmd
        // runOut => 与 runner 的 stdout 连接的 pipe
        // runErr => 与 runner 的 stderr 连接的 pipe
		runOut, runErr, err := run.GetOutputs(runner)
		if err != nil {
			return err
		}

		if t.Register == "" {
            // 将 runner 的标准输出重定向到 stdout
			go io.Copy(stdout, runOut)
		} else {
            // 将 runner 的标准输出重定向到 外部存储
			go io.Copy(register, runOut)
		}
        // 将 runner 的标准错误重定向到 stderr
		go io.Copy(stderr, runErr)

		startTime = time.Now()

        // Note: 这段代码等价于 exec.Run() 的实现
        /////////
		err = runner.Start()
		if err != nil {
			return err
		}

		err = runner.Wait()
        /////////
		if err != nil {
			if exitError, ok := err.(*exec.ExitError); ok {
				if waitStatus, ok := exitError.Sys().(syscall.WaitStatus); !ok {
					exitStatus = 1 // For unknown error exit codes set it to 1
				} else {
					exitStatus = waitStatus.ExitStatus()
				}
			}
			if ProvideExtraInfo {
				fmt.Printf("-- %s (%s) failed with exit status %d\n",
					t.Name, time.Since(startTime).String(), exitStatus)
			}
			return err
		}

        // 若定义了 Register 则 task 之间基于 register 进行传值
		if t.Register != "" {
			r := fmt.Sprintf("%s=%s", t.Register, register.String())
			registers = append(registers, strings.TrimSpace(r))
		}

		if ProvideExtraInfo {
			fmt.Printf("-- %s (%s) finished with exit status %d\n",
				t.Name, time.Since(startTime).String(), exitStatus)
		}

	}
	return nil
}
