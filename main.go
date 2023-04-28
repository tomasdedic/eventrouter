package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/golang/glog"
	"github.com/spf13/viper"
	v1 "k8s.io/api/core/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/informers"
	coreinformers "k8s.io/client-go/informers/core/v1"
	"k8s.io/client-go/kubernetes"
	corelisters "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"veryraven.com/eventrouter/sink"
)

func sigHandler() <-chan struct{} {
	stop := make(chan struct{})
	//trap signals
	go func() {
		c := make(chan os.Signal, 1)
		signal.Notify(c,
			syscall.SIGINT,  // Ctrl+C
			syscall.SIGTERM, // Termination Request
			syscall.SIGSEGV, // FullDerp
			syscall.SIGABRT, // Abnormal termination
			syscall.SIGILL,  // illegal instruction
			syscall.SIGFPE)  // floating point - this is why we can't have nice things
		sig := <-c
		glog.Warningf("Signal (%v) Detected, Shutting Down", sig)
		close(stop)
	}()
	return stop
}

func loadConfig() kubernetes.Interface {
	var config *rest.Config
	var err error

	viper.SetConfigType("yaml")
	viper.SetConfigName("config")
	viper.AddConfigPath(".")
	if err = viper.ReadInConfig(); err != nil {
		panic(err.Error())
	}
	viper.BindEnv("kubeconfig")
	kubeconfig := viper.GetString("kubeconfig")
	if len(kubeconfig) > 0 {
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
	} else {
		config, err = rest.InClusterConfig()
	}
	if err != nil {
		panic(err.Error())
	}
	//create client from kubeconfig
	clientset, err := kubernetes.NewForConfig(config)

	if err != nil {
		panic(err.Error())
	}
	return clientset
}

// eventRouter definition
// we will watch on Add events for Events and lookup for namespaces labels
type EventRouter struct {
	kubeClient      kubernetes.Interface
	eventLister     corelisters.EventLister
	eventListerSync cache.InformerSynced
	nsLister        corelisters.NamespaceLister
	nsListerSync    cache.InformerSynced
	eventSink       sink.Sink
}

//create new NewEventRouter with informers for Events and Namespaces
func NewEventRouter(kubeClient kubernetes.Interface, eventsInformer coreinformers.EventInformer, nsInformer coreinformers.NamespaceInformer) *EventRouter {
	eventrouter := &EventRouter{
		kubeClient: kubeClient,
	}
	eventsInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    eventrouter.onAdd,
		UpdateFunc: eventrouter.onUpdate,
		// DeleteFunc: func(interface{}) { fmt.Println("delete not implemented") },
	})
	eventrouter.eventLister = eventsInformer.Lister()
	eventrouter.eventListerSync = eventsInformer.Informer().HasSynced
	eventrouter.nsLister = nsInformer.Lister()
	eventrouter.nsListerSync = eventsInformer.Informer().HasSynced
	return eventrouter
}

// onAdd handler is called when an event is created, or during the initial list
// func (er *EventRouter) onAdd(obj interface{}) {
// 	event := obj.(*v1.Event)
// 	namespace, err := er.nsLister.Get(event.Namespace)
// 	if err != nil {
// 		glog.Warning(err)
// 	}
// 	for labell, labelr := range namespace.Labels {
// 		fmt.Printf("%s=%s,", labell, labelr)
// 	}
//
// 	//filter labels here
// 	// newEvent := newEventWithNsLables(*event, namespace.Labels)
//
// 	// fmt.Println(namespace.Labels)
// 	fmt.Println(event.Message)
// }

func (er *EventRouter) onAdd(obj interface{}) {
	event := obj.(*v1.Event)
	namespace, err := er.nsLister.Get(event.Namespace)
	if err != nil {
		glog.Warning(err)
	}
	er.eventSink.UpdateEvents(event, nil, namespace.Labels)
}

func (er *EventRouter) onUpdate(objOld interface{}, objNew interface{}) {
	eventOld := objOld.(*v1.Event)
	eventNew := objNew.(*v1.Event)
	namespace, err := er.nsLister.Get(eventNew.Namespace)
	if err != nil {
		glog.Warning(err)
	}
	er.eventSink.UpdateEvents(eventNew, eventOld, namespace.Labels)
}

// updateEvent is called any time there is an update to an existing event
// func (er *EventRouter) onUpdate(objOld interface{}, objNew interface{}) {
// 	eventNew := objNew.(*v1.Event)
// 	fmt.Println(eventNew.Message)
// }

// Run starts the EventRouter/Controller.
func (er *EventRouter) Run(stopCh <-chan struct{}) {
	defer utilruntime.HandleCrash()
	defer glog.Infof("Shutting down EventRouter")

	glog.Infof("Starting EventRouter")

	// wait for cache sync
	if !cache.WaitForCacheSync(stopCh, er.eventListerSync) {
		utilruntime.HandleError(fmt.Errorf("timed out waiting for caches to sync"))
		return
	}
	if !cache.WaitForCacheSync(stopCh, er.nsListerSync) {
		utilruntime.HandleError(fmt.Errorf("timed out waiting for caches to sync"))
		return
	}
	<-stopCh
}

func main() {
	flag.Parse() //parse glog command line options, log all messages to stderr with -logtostderr=true
	var wg sync.WaitGroup

	// load config for connection
	clientset := loadConfig()
	//different sync time for Events and Namespace
	eventsSharedInformers := informers.NewSharedInformerFactory(clientset, 10*time.Second)
	nsSharedInformers := informers.NewSharedInformerFactory(clientset, 30*time.Second)

	eventsInformer := eventsSharedInformers.Core().V1().Events()
	nsInformer := nsSharedInformers.Core().V1().Namespaces()

	eventRouter := NewEventRouter(clientset, eventsInformer, nsInformer)
	stop := sigHandler()
	wg.Add(1)
	go func() {
		defer wg.Done()
		eventRouter.Run(stop)
	}()

	// Startup the Informer(s)
	glog.Infof("Starting shared Informer(s)")
	eventsSharedInformers.Start(stop)
	nsSharedInformers.Start(stop)
	wg.Wait()
	glog.Warningf("Exiting main()")
	os.Exit(1)
}
