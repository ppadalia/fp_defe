package sched

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v2"
)

type testCounter struct {
	sync.Mutex
	count int
}

func (tc *testCounter) incr() {
	tc.Lock()
	tc.count++
	tc.Unlock()
}

func TestStartStop(t *testing.T) {
	s := New(time.Second)
	s.Stop()
	require.True(t, true, "OK")
}

func TestSingle(t *testing.T) {
	s := New(time.Second)
	tc := testCounter{count: 0}
	task := func(Interval) {
		tc.incr()
	}

	s.Schedule(task, Periodic(2*time.Second), time.Now(), true)
	time.Sleep(6 * time.Second)
	require.Equal(t, tc.count, 1, "running only once")

	taskID, err := s.Schedule(task, Periodic(2*time.Second), time.Now(), false)
	require.Equal(t, err, nil, "schedule periodic")
	time.Sleep(12 * time.Second)
	require.True(t, tc.count <= 6, "periodic task")
	s.Stop()
	time.Sleep(3 * time.Second)
	stopCount := tc.count
	time.Sleep(4 * time.Second)
	require.True(t, tc.count == stopCount, "task scheduled when stopped")

	err = s.Cancel(taskID)
	require.Equal(t, err, nil, "cancel valid task")
	time.Sleep(6 * time.Second)
	require.True(t, tc.count < 7, "periodic task after stop")

	s.Stop()
}

func TestCancel(t *testing.T) {
	s := New(time.Second)
	tc := testCounter{count: 0}
	task := func(Interval) {
		tc.incr()
	}

	taskID, err := s.Schedule(task, Periodic(10*time.Second), time.Now(), true)
	require.Equal(t, err, nil, "scheduling task")
	time.Sleep(8 * time.Second)

	require.Equal(t, tc.count, 0, "task should not be scheduled")
	err = s.Cancel(taskID)
	require.Equal(t, err, nil, "cancel future task")

	time.Sleep(2 * time.Second)
	require.Equal(t, tc.count, 0, "cancelled task")
}

func TestMulti(t *testing.T) {
	s := New(time.Second)

	// Create a bunch of task counters and tasks.
	tcs := make([]*testCounter, 0)
	tasks := make([]func(Interval), 0)

	for i := 0; i < 10; i++ {
		tc := &testCounter{count: 0}
		task := func(Interval) {
			tc.incr()
		}
		tcs = append(tcs, tc)
		tasks = append(tasks, task)
	}

	// Schedule some tasks - few as runOnce and others as periodic.
	taskIDs := make([]TaskID, 0)
	for i, _ := range tasks {
		taskID, err := s.Schedule(tasks[i], Periodic(2*time.Second), time.Now(),
			i%3 == 0)
		require.Equal(t, err, nil, "schedule multi")
		taskIDs = append(taskIDs, taskID)
	}
	time.Sleep(3500 * time.Millisecond)

	// all tasks should have been run once already
	for i, tc := range tcs {
		require.True(t, tc.count == 1, "count was %d for task %d", tc.count, i)
	}
	time.Sleep(16500 * time.Millisecond)

	// Check counters for runOnce and periodic, cancel the runOnce tasks.
	for i, tc := range tcs {
		if i%3 == 0 {
			require.True(t, tc.count == 1, "count for runOnce task")
			err := s.Cancel(taskIDs[i])
			require.NotEqual(t, err, nil, "cancelling runOnce task")
		} else {
			require.True(t, tc.count > 4, "periodic multi-task")
		}
	}

	// Stop the schedular and see that tasks are not scheduled anymore.
	s.Stop()
	time.Sleep(2 * time.Second)

	counters := make([]int, len(tcs))
	for i, tc := range tcs {
		counters[i] = tc.count
	}
	time.Sleep(6 * time.Second)
	for i, tc := range tcs {
		require.Equal(t, counters[i], tc.count,
			"counters increased after stopping schedular")
	}

	// Start the schedular and see tasks get scheduled
	s.Start()
	time.Sleep(4 * time.Second)

	for i, tc := range tcs {
		runOnce := i % 3
		if runOnce != 0 {
			require.True(t, tc.count == counters[i],
				"counters increased after stopping schedular")
			err := s.Cancel(taskIDs[i])
			require.Equal(t, err, nil, "cancelling multi-task")
		}
	}

	for _, taskID := range taskIDs {
		err := s.Cancel(taskID)
		require.NotEqual(t, err, nil, "cancelling invalid multi-task")
	}

	s.Stop()
}

func TestWorkersAddRemove(t *testing.T) {
	s := New(time.Second)

	// Create a bunch of task counters and tasks.
	tcs := make([]*testCounter, 0)
	tasks := make([]func(Interval), 0)

	for i := 0; i < 10*maxWorkers; i++ {
		tc := &testCounter{count: 0}
		task := func(Interval) {
			tc.incr()
			time.Sleep(2 * time.Second) // each task runs for 2 seconds
		}
		tcs = append(tcs, tc)
		tasks = append(tasks, task)
	}

	// Schedule 3*workerBatchSize number of tasks so that the scheduler creates one new batch of workers.
	for i := 0; i < 3*workerBatchSize; i++ {
		_, err := s.Schedule(tasks[i], Periodic(2*time.Second), time.Now(), true)
		require.Equal(t, nil, err, "failed to schedule a task")
	}
	time.Sleep(3500 * time.Millisecond)

	// 2*workerBatchSize number of tasks should have been run once already due to the addition of new workers
	for i, tc := range tcs {
		if i < 2*workerBatchSize {
			require.True(t, tc.count == 1, "count was %d for task %d, expected 1", tc.count, i)
		} else {
			require.True(t, tc.count == 0, "count was %d for task %d, expected 0", tc.count, i)
		}
	}
	m := s.(*manager)
	require.Equal(t, 2*workerBatchSize, m.getWorkerCount(), "new workers were not started")

	// after the idle timeout, half of the workers should exit
	time.Sleep(idleTimeout + 3*time.Second)
	require.Equal(t, workerBatchSize, m.getWorkerCount(), "workers did not time out after being idle")

	// schedule a large number of tasks to make the workers max out
	for i := 0; i < 10*maxWorkers; i++ {
		_, err := s.Schedule(tasks[i], Periodic(2*time.Second), time.Now(), true)
		require.Equal(t, nil, err, "failed to schedule a task")
	}
	time.Sleep(15 * time.Second)

	// number of workers should have maxed out
	require.Equal(t, maxWorkers, m.getWorkerCount(), "workers did not max out")

	s.Stop()
}

func TestScheduleStrings(t *testing.T) {
	invalidSchedules := make(map[string][]string)
	invalidSchedules[DailyType] = []string{"10.15", "10.15,a", "10.15,", ",a",
		",5", ","}
	invalidSchedules[WeeklyType] = []string{"x", ",5", ",a5", "a,5", "Mon,5",
		"monday@1.4,5", "monday@1,5.5"}
	invalidSchedules[MonthlyType] = []string{"x", ",5", ",a5", "a,5",
		"10,", "10.4@", "10@1.2", "10@1,4.5", "10,1.1", "40@", "50", "@11.,55",
		"@11.,5.5", "10.1@11.,5.5", "10@11.,5", "40@11,5"}
	invalidSchedules[PeriodicType] = []string{"", ",", "x", ",x", "x,", "10.4",
		"10.4,a", "10,a", "10,4.4", "1.2,5"}

	ParseCLI[PeriodicType] = ParsePeriodic

	for schedType, intvs := range invalidSchedules {
		parser, _ := ParseCLI[schedType]
		for _, intv := range intvs {
			_, err := parser(intv)
			require.NotEqual(t, err, nil, "%s parsed as valid %s",
				intv, schedType)
		}
	}

	dockSched := []string{
		DailyType + nonYamlTypeSeparator + "10:43,5",
		MonthlyType + nonYamlTypeSeparator + "10@13:30,5",
		WeeklyType + nonYamlTypeSeparator + "monday@10:52,5",
		PeriodicType + nonYamlTypeSeparator + "10,5",
		policyTag + nonYamlTypeSeparator + "x1,x2,x3",
	}
	origLen := len(dockSched)
	for i := 0; i < origLen; i++ {
		for j := i + 1; j < origLen; j++ {
			dockSched = append(dockSched, dockSched[i]+scheduleSeparator+
				dockSched[j])
		}
	}
	for i, sched := range dockSched {
		ivs, _, err := ParseScheduleAndPolicies(sched)
		require.Equal(t, err, nil, "Parsing policy %s, err: %v", sched, err)
		require.NoError(t, err)
		perDay := MaxPerDayInstances(ivs)
		if len(ivs) == 0 || i >= origLen {
			continue
		}
		switch ivs[0].Spec().Freq {
		case PeriodicType:
			// every 10mins, 6 per hour and 144 per day
			require.Equal(t, perDay, uint32(144), "Incorrect periodic intervals per day")
		case DailyType:
			require.Equal(t, perDay, uint32(1), "Incorrect daily intervals per day")
		case WeeklyType:
			require.Equal(t, perDay, uint32(1), "Incorrect Weekly intervals per day")
		case MonthlyType:
			require.Equal(t, perDay, uint32(1), "Incorrect Monthly intervals per day")
		}
	}
	cumulativeSched := strings.Join(dockSched[0:4], scheduleSeparator)
	ivs, _, err := ParseScheduleAndPolicies(cumulativeSched)
	require.NoError(t, err)
	perDay := MaxPerDayInstances(ivs)
	require.Equal(t, perDay, uint32(147), "Unexpcted number of intervals per day")

}

func TestScheduleUpgrade(t *testing.T) {
	d := Daily(1, 2)
	m := Monthly(10, 4, 54)
	w := Weekly(time.Friday, 14, 30)
	p := Periodic(120 * time.Minute)
	oldSpecs := []IntervalSpec{
		d.Spec(), m.Spec(), w.Spec(), p.Spec(),
	}
	oldSpecString, err := yaml.Marshal(oldSpecs)
	str := string(oldSpecString)
	_, _, err = ParseScheduleAndPolicies(str)
	require.Equal(t, err, nil, "Parsing old intervals %v", err)
}
