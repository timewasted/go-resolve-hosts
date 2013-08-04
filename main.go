// Copyright 2013 Ryan Rogers. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"flag"
	"io/ioutil"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const numResolvers = 4
const matchSuffix = ".hosts"

type resolveHostInfo struct {
	path, host string
	ip         []net.IP
	err        error
}
type hostAddressInfo struct {
	hosts []resolveHostInfo
	sync.Mutex
}

var resolveChannel chan resolveHostInfo
var resolvedHosts hostAddressInfo

var walkDir = flag.String("dir", ".", "The directory to look in for host files to resolve.")

func init() {
	log.SetPrefix("resolve-hosts: ")

	flag.Parse()
	*walkDir = filepath.Clean(*walkDir)
	// Make sure that the specified directory exists, and is actually a directory.
	if fileInfo, err := os.Stat(*walkDir); err != nil || !fileInfo.IsDir() {
		log.Println(err)
		flag.PrintDefaults()
		os.Exit(1)
	}
}

func main() {
	// Set up channels and spin up resolvers.
	resolveChannel = make(chan resolveHostInfo, numResolvers*2)
	defer close(resolveChannel)
	processingHostChannel := make(chan interface{}, numResolvers+1)
	defer close(processingHostChannel)

	for i := 0; i < numResolvers; i++ {
		go resolveHost(resolveChannel, processingHostChannel)
	}

	// Parse host files found in the specified directory.
	filepath.Walk(*walkDir, parseHostFiles)
	for {
		if len(resolveChannel) == 0 && len(processingHostChannel) == 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Write the resolved hosts to disk.
	outputFiles := make(map[string]*os.File)
	errorFiles := make(map[string]interface{})
	for _, host := range resolvedHosts.hosts {
		// If writing to a file has failed, don't continue trying to write to it.
		if _, exists := errorFiles[host.path]; exists {
			continue
		}

		// Attempt to open the file, and store the handle for re-use.
		file, open := outputFiles[host.path]
		if !open {
			file, err := os.Create(host.path)
			if err != nil {
				errorFiles[host.path] = nil
				log.Println(err)
				continue
			}
			outputFiles[host.path] = file
		}
		if file == nil {
			file = outputFiles[host.path]
		}

		// Write the output string to the file.
		var output string
		if host.err != nil {
			output = "# " + host.err.Error() + "\n"
		} else {
			output = "# " + host.host + "\n"
			for _, ip := range host.ip {
				output += ip.String() + "\n"
			}
		}
		n, err := file.WriteString(output)
		if err != nil {
			errorFiles[host.path] = nil
			log.Println(err)
		} else if n != len(output) {
			errorFiles[host.path] = nil
			log.Printf("%s: expected to write %d bytes, actually wrote %d.\n", host.path, len(output), n)
		}
	}
	for _, file := range outputFiles {
		file.Close()
	}
}

func parseHostFiles(path string, info os.FileInfo, err error) error {
	// FIXME: Is this check needed?
	if err != nil {
		return err
	}
	// Ignore directories except the base directory.
	if info.IsDir() && path != *walkDir {
		return filepath.SkipDir
	}
	// Ignore files that don't match the desired suffix.
	if matched, err := filepath.Match("*"+matchSuffix, filepath.Base(path)); !matched || err != nil {
		return nil
	}
	// Ignore files that have an exact name of the desired suffix.
	if matched, err := filepath.Match(matchSuffix, filepath.Base(path)); matched || err != nil {
		return nil
	}

	contents, err := ioutil.ReadFile(path)
	if err != nil {
		return err
	}
	lines := strings.Split(string(contents), "\n")
	for _, host := range lines {
		host = strings.TrimSpace(host)
		// Ignore blank lines, or lines that are comments.
		if len(host) == 0 || host[0] == '#' {
			continue
		}
		resolveChannel <- resolveHostInfo{path[:len(path)-len(matchSuffix)], host, nil, nil}
	}

	return nil
}

func resolveHost(resolve <-chan resolveHostInfo, processing chan interface{}) {
	for {
		// FIXME: There's a possible race condition here.
		hostInfo := <-resolve
		processing <- nil
		hostInfo.ip, hostInfo.err = net.LookupIP(hostInfo.host)
		resolvedHosts.Lock()
		resolvedHosts.hosts = append(resolvedHosts.hosts, hostInfo)
		resolvedHosts.Unlock()
		<-processing
	}
}
