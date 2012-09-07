
package shard

import (
	"fmt"
	"os"
	"time"
	"path/filepath"
	"encoding/gob"
)

func (self *Shard) addSlave(snapshot, stream chan Operation) {
	self.slaveChannel <- slave{snapshot, stream}
}
func (self *Shard) setMaster(snapshot, stream chan Operation) {
	self.stopSlavery()
	response := &Response{}
	self.Perform(Operation{CLEAR, []string{}}, response)
	if response.Result & OK != OK {
		panic(fmt.Errorf("When trying to clear: %v", response))
	}
	self.masterSnapshot = snapshot
	self.masterStream = stream
	sem := newSemaphore()
	self.masterStreamSem = sem
	go self.bufferMaster(stream, sem)
	go self.followMaster(snapshot, sem)
}
func (self *Shard) followMaster(snapshot chan Operation, sem *semaphore) {
	response := &Response{}
	for op := range snapshot {
		if self.isClosed() {
			return 
		}
		self.Perform(op, response)
		if response.Result & OK != OK {
			panic(fmt.Errorf("While trying to perform %v: %v", op, response))
		}
	}
	sem.wait()
	path, t := self.getOldestFollow()
	decoder := newFileDecoder(filepath.Join(self.dir, path))
	for self.masterSnapshot == snapshot {
		self.loadDecoder(decoder)
		next_path, next_t, ok := self.getNextFollow(t)
		if ok {
			path = next_path
			t = next_t
			decoder = newFileDecoder(filepath.Join(self.dir, path))
		} else {
			sem.wait()
		}
	}
}
func (self *Shard) stopSlavery() {
	if snapshot := self.masterSnapshot; snapshot != nil {
		close(snapshot)
	}
	if stream := self.masterStream; stream != nil {
		close(stream)
	}
	if sem := self.masterStreamSem; sem != nil {
		sem.broadcast()
	}
	self.masterSnapshot = nil
	self.masterStream = nil
	self.masterStreamSem = nil
	for _, log := range self.getLogs(followPattern) {
		os.Remove(filepath.Join(self.dir, log))
	}
}
func (self *Shard) bufferMaster(stream chan Operation, sem *semaphore) {
	logfile := self.newLogFile(time.Now(), followFormat)
	sem.broadcast()
	encoder := gob.NewEncoder(logfile)
	for op := range self.masterStream {
		if self.isClosed() {
			return 
		}
		if err := encoder.Encode(op); err != nil {
			panic(fmt.Errorf("While trying to log %v: %v", op, err))
		}
		sem.broadcast()
		if self.tooLargeLog(logfile) {
			logfile.Close()
			logfile = self.newLogFile(time.Now(), followFormat)
			encoder = gob.NewEncoder(logfile)
		}
	}
}