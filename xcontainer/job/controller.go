package job

import (
	"github.com/qixi7/xengine_core/xcontainer/channel"
	"github.com/qixi7/xengine_core/xmodule"
	"sync"
)

type Controller struct {
	jobChan  channel.UnBlockSPMC
	doneChan chan Done
	goNum    int
	jobNum   int
	wg       sync.WaitGroup
}

func NewController(bufLen int, goNum int) *Controller {
	ctrl := &Controller{
		goNum:    goNum,
		jobNum:   0,
		doneChan: make(chan Done, bufLen),
	}
	ctrl.jobChan.Init(bufLen)
	for i := 0; i < goNum; i++ {
		ctrl.wg.Add(1)
		go ctrl.doJob(i)
	}
	return ctrl
}

func (ctrl *Controller) doJob(idx int) {
	defer func() {
		ctrl.wg.Done()
	}()

	for {
		iJob, ok := ctrl.jobChan.Read()
		if !ok {
			return
		}
		job := iJob.(Do)
		done := job.DoJob()
		if done != nil {
			ctrl.doneChan <- done
		}
	}
}

func (ctrl *Controller) Stop() {
	ctrl.jobChan.Close()
	ctrl.wg.Wait()
}

func (ctrl *Controller) PostJob(job Do) bool {
	ctrl.jobChan.Write(job)
	ctrl.jobNum++
	return true
}

func (ctrl *Controller) ProcessReturn() {
	batch := 25
	for i := 0; i < batch; i++ {
		select {
		case iJob := <-ctrl.doneChan:
			ctrl.jobNum--
			job := iJob.(Done)
			job.DoReturn()
		default:
			return
		}
	}
}

func (ctrl *Controller) ProcessWaitReturn() {
	for ctrl.jobNum > 0 {
		select {
		case iJob := <-ctrl.doneChan:
			ctrl.jobNum--
			job := iJob.(Done)
			job.DoReturn()
		}
	}
}

// 获取job数量
func (ctrl *Controller) GetJobNum() int {
	return ctrl.jobNum
}

func (ctrl *Controller) Init(selfGetter xmodule.DModuleGetter) bool {
	return true
}

func (ctrl *Controller) Run(delta int64) {
	ctrl.ProcessReturn()
}

func (ctrl *Controller) Destroy() {
	ctrl.Stop()
}
