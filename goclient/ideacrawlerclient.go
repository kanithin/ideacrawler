/*************************************************************************
 *
 * Copyright 2018 Ideas2IT Technology Services Private Limited.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 ***********************************************************************/

package goclient

import (
	"errors"
	"fmt"
	google_protobuf1 "github.com/golang/protobuf/ptypes/duration"
	google_protobuf "github.com/golang/protobuf/ptypes/timestamp"
	pb "github.com/ideas2it/ideacrawler/protofiles"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"io"
	"log"
	"sync"
	"time"
)

type PageHTML = pb.PageHTML

type CrawlJob struct {
	SeedURL                 string
	MinDelay                int32
	MaxDelay                int32
	Follow                  bool
	CallbackUrlRegexp       string
	FollowUrlRegexp         string
	CallbackXpathMatch      []*pb.KVP
	CallbackXpathRegexp     []*pb.KVP
	MaxConcurrentRequests   int32
	Useragent               string
	Impolite                bool
	Depth                   int32
	Repeat                  bool
	Frequency               *google_protobuf1.Duration
	Firstrun                *google_protobuf.Timestamp
	UnsafeNormalizeURL      bool
	Login                   bool
	LoginUrl                string
	LoginJS                 string
	LoginPayload            []*pb.KVP
	LoginParseFields        bool
	LoginParseXpath         []*pb.KVP
	LoginSuccessCheck       *pb.KVP
	CheckLoginAfterEachPage bool
	Chrome                  bool
	ChromeBinary            string
	DomLoadTime             int32
	NetworkIface            string
	CancelOnDisconnect      bool
	CheckContent            bool
	Prefetch                bool
	UseAnchorText           bool
	WaitGroup               *sync.WaitGroup

	dopt           *pb.DomainOpt
	svrHost        string
	svrPort        string
	running        bool
	client         pb.IdeaCrawlerClient
	addPagesClient pb.IdeaCrawler_AddPagesClient
	sub            *pb.Subscription

	Callback     func(*PageHTML, *CrawlJob)
	usePageChan  bool
	PageChan     <-chan *pb.PageHTML
	implPageChan chan *pb.PageHTML

	implAnalyzedURLChan chan *pb.UrlList
	AnalyzedURLChan     <-chan *pb.UrlList
}

func NewCrawlJob(svrHost, svrPort string) *CrawlJob {
	var cj = &CrawlJob{}

	cj.MinDelay = 5
	cj.Follow = true
	cj.Depth = -1
	cj.DomLoadTime = 5
	cj.Useragent = "Fetchbot"
	cj.MaxConcurrentRequests = 5

	cj.svrHost = svrHost
	cj.svrPort = svrPort

	return cj
}

type KVMap = map[string]string

const (
	PageReqType_BUILTINJS = pb.PageReqType_BUILTINJS
	PageReqType_JSCRIPT   = pb.PageReqType_JSCRIPT
)

func (cj *CrawlJob) SetLogin(loginUrl string, loginPayload, loginParseXpath KVMap, loginSuccessCheck KVMap) {
	cj.Login = true
	cj.LoginUrl = loginUrl
	for k, v := range loginPayload {
		cj.LoginPayload = append(cj.LoginPayload, &pb.KVP{k, v})
	}
	if len(loginParseXpath) > 0 {
		cj.LoginParseFields = true
	}
	for k, v := range loginParseXpath {
		cj.LoginParseXpath = append(cj.LoginParseXpath, &pb.KVP{k, v})
	}

	for k, v := range loginSuccessCheck {
		cj.LoginSuccessCheck = &pb.KVP{k, v}
	}
}

func (cj *CrawlJob) SetLoginChrome(loginUrl string, loginJS string, loginSuccessCheck KVMap) {
	cj.Login = true
	cj.LoginUrl = loginUrl
	cj.LoginJS = loginJS
	for k, v := range loginSuccessCheck {
		cj.LoginSuccessCheck = &pb.KVP{k, v}
	}
}

func (cj *CrawlJob) SetCallbackXpathMatch(mdata KVMap) {
	for k, v := range mdata {
		cj.CallbackXpathMatch = append(cj.CallbackXpathMatch, &pb.KVP{k, v})
	}
}

func (cj *CrawlJob) SetCallbackXpathRegexp(mdata KVMap) {
	for k, v := range mdata {
		cj.CallbackXpathRegexp = append(cj.CallbackXpathRegexp, &pb.KVP{k, v})
	}
}

func (cj *CrawlJob) SetPageChan(pageChan chan *pb.PageHTML) {
	cj.usePageChan = true
	cj.implPageChan = pageChan
	cj.PageChan = cj.implPageChan
}

func (cj *CrawlJob) SetAnalyzedURL(analyzedURLChan chan *pb.UrlList) {

	if cj.sub == nil {
		log.Println("No job subscription. SetAnalyzedURLs failed.")
		return
	}
	urlstream, err := cj.client.GetAnalyzedURLs(context.Background(), cj.sub, grpc.MaxCallRecvMsgSize((2*1024*1024*1024)-1))
	if err != nil {
		log.Println("Box is possibly down. SetAnalyzedURLs failed:", err)
		return
	}
	cj.implAnalyzedURLChan = analyzedURLChan
	cj.AnalyzedURLChan = cj.implAnalyzedURLChan
	go cj.listenAnalyzedURLs(urlstream)
}

func (cj *CrawlJob) listenAnalyzedURLs(urlstream pb.IdeaCrawler_GetAnalyzedURLsClient) {
	for {
		urlList, err := urlstream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			fmt.Println(err)
			break
		}
		cj.implAnalyzedURLChan <- urlList
	}
}

func (cj *CrawlJob) AddPage(url, metaStr string) error {
	if cj.IsAlive() == false {
		errorMsg := "AddPage function can't be called when crawl job is not running. Please start crawl job first then call Addpage."
		log.Println(errorMsg)
		return errors.New(errorMsg)
	}
	if cj.addPagesClient == nil {
		var err error
		cj.addPagesClient, err = cj.client.AddPages(context.Background())
		if err != nil {
			cj.addPagesClient = nil
			return err
		}
	}
	return cj.addPagesClient.Send(&pb.PageRequest{
		Sub:     cj.sub,
		Reqtype: pb.PageReqType_GET,
		Url:     url,
		MetaStr: metaStr,
	})
}

func (cj *CrawlJob) AddJS(typ pb.PageReqType, url, js, metaStr string) error {
	if cj.IsAlive() == false {
		errorMsg := "AddJS function can't be called when crawl job is not running. Please start crawl job first then call AddJS."
		log.Println(errorMsg)
		return errors.New(errorMsg)
	}
	if cj.addPagesClient == nil {
		var err error
		cj.addPagesClient, err = cj.client.AddPages(context.Background())
		if err != nil {
			cj.addPagesClient = nil
			return err
		}
	}
	return cj.addPagesClient.Send(&pb.PageRequest{
		Sub:     cj.sub,
		Reqtype: typ,
		Url:     url,
		Js:      js,
		MetaStr: metaStr,
	})
}

func (cj *CrawlJob) Start() {
	go cj.Run()
	time.Sleep(2 * time.Second)
}

func (cj *CrawlJob) IsAlive() bool {
	return cj.running
}

func (cj *CrawlJob) Stop() {
	if cj.IsAlive() {
		cj.client.CancelJob(context.Background(), cj.sub)
	}
}

func (cj *CrawlJob) OnFinish() {
	cj.WaitGroup.Done()
}

func (cj *CrawlJob) Run() {
	cj.running = true
	defer func() {
		cj.running = false
		cj.OnFinish()
	}()

	if cj.usePageChan == true && cj.Callback != nil {
		log.Fatal("Callback channel and function both can't be used at the same time")
	} else if cj.usePageChan == false && cj.Callback == nil {
		log.Fatal("Please set pageChan to get callbacks on,  or provide a callback function")
	}

	var opts []grpc.DialOption
	opts = append(opts, grpc.WithInsecure())
	conn, err := grpc.Dial(cj.svrHost+":"+cj.svrPort, opts...)
	if err != nil {
		log.Fatalf("fail to dial: %v", err)
	}
	defer conn.Close()
	cj.client = pb.NewIdeaCrawlerClient(conn)

	cj.dopt = &pb.DomainOpt{
		SeedUrl:                 cj.SeedURL,
		MinDelay:                cj.MinDelay,
		MaxDelay:                cj.MaxDelay,
		NoFollow:                !cj.Follow,
		CallbackUrlRegexp:       cj.CallbackUrlRegexp,
		FollowUrlRegexp:         cj.FollowUrlRegexp,
		CallbackXpathMatch:      cj.CallbackXpathMatch,
		CallbackXpathRegexp:     cj.CallbackXpathRegexp,
		MaxConcurrentRequests:   cj.MaxConcurrentRequests,
		Useragent:               cj.Useragent,
		Impolite:                cj.Impolite,
		Depth:                   cj.Depth,
		Repeat:                  cj.Repeat,
		Frequency:               cj.Frequency,
		Firstrun:                cj.Firstrun,
		UnsafeNormalizeURL:      cj.UnsafeNormalizeURL,
		Login:                   cj.Login,
		LoginUrl:                cj.LoginUrl,
		LoginJS:                 cj.LoginJS,
		LoginPayload:            cj.LoginPayload,
		LoginParseFields:        cj.LoginParseFields,
		LoginParseXpath:         cj.LoginParseXpath,
		LoginSuccessCheck:       cj.LoginSuccessCheck,
		CheckLoginAfterEachPage: cj.CheckLoginAfterEachPage,
		Chrome:                  cj.Chrome,
		ChromeBinary:            cj.ChromeBinary,
		DomLoadTime:             cj.DomLoadTime,
		NetworkIface:            cj.NetworkIface,
		CancelOnDisconnect:      cj.CancelOnDisconnect,
		CheckContent:            cj.CheckContent,
		Prefetch:                cj.Prefetch,
		UseAnchorText:           cj.UseAnchorText,
	}
	pagestream, err := cj.client.AddDomainAndListen(context.Background(), cj.dopt, grpc.MaxCallRecvMsgSize((2*1024*1024*1024)-1))
	if err != nil {
		log.Println("Box is possibly down. AddDomainAndListen failed:", err)
		return
	}
	subpage, err := pagestream.Recv()
	if err == io.EOF {
		return
	}
	if err != nil {
		fmt.Println(err)
		return
	}
	cj.sub = subpage.Sub
	phChan := make(chan *pb.PageHTML, 1000)
	defer close(phChan)

	go func() {
		time.Sleep(3 * time.Second) // This is to make sure callbacks don't start until Start() function exits.  Start sleep for 2 seconds.
		if cj.usePageChan {
			for ph := range phChan {
				cj.implPageChan <- ph
			}
		} else {
			for ph := range phChan {
				cj.Callback(ph, cj)
			}
		}
	}()

	for {
		page, err := pagestream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			fmt.Println(err)
			break
		}
		phChan <- page
	}
}

func NewPageChan() chan *pb.PageHTML {
	return make(chan *pb.PageHTML, 100)
}

func NewAnalyzedURLChan() chan *pb.UrlList {
	return make(chan *pb.UrlList, 100)
}
