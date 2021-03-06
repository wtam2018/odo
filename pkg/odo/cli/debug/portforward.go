package debug

import (
	"fmt"
	"github.com/openshift/odo/pkg/config"
	"github.com/openshift/odo/pkg/debug"
	"github.com/openshift/odo/pkg/log"
	"github.com/openshift/odo/pkg/odo/genericclioptions"
	"github.com/openshift/odo/pkg/odo/util/experimental"
	"github.com/openshift/odo/pkg/util"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/spf13/cobra"

	k8sgenclioptions "k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/kubectl/pkg/util/templates"
)

// PortForwardOptions contains all the options for running the port-forward cli command.
type PortForwardOptions struct {
	componentName   string
	applicationName string
	Namespace       string

	// PortPair is the combination of local and remote port in the format "local:remote"
	PortPair string

	localPort  int
	contextDir string

	PortForwarder *debug.DefaultPortForwarder
	// StopChannel is used to stop port forwarding
	StopChannel chan struct{}
	// ReadChannel is used to receive status of port forwarding ( ready or not ready )
	ReadyChannel chan struct{}
	*genericclioptions.Context
	DevfilePath string

	isExperimental bool
}

var (
	portforwardLong = templates.LongDesc(`
			Forward a local port to a remote port on the pod where the application is listening for a debugger.

			By default the local port and the remote port will be same. To change the local port you can use --local-port argument and to change the remote port use "odo config set DebugPort <port>"   		  
	`)

	portforwardExample = templates.Examples(`
		# Listen on default port and forwarding to the default port in the pod
		odo debug port-forward 

		# Listen on the 5000 port locally, forwarding to default port in the pod
		odo debug port-forward --local-port 5000
		
		`)
)

const (
	portforwardCommandName = "port-forward"
)

func NewPortForwardOptions() *PortForwardOptions {
	return &PortForwardOptions{}
}

// Complete completes all the required options for port-forward cmd.
func (o *PortForwardOptions) Complete(name string, cmd *cobra.Command, args []string) (err error) {

	var remotePort int

	o.isExperimental = experimental.IsExperimentalModeEnabled()

	if o.isExperimental && util.CheckPathExists(o.DevfilePath) {
		o.Context = genericclioptions.NewDevfileContext(cmd)

		// a small shortcut
		env := o.Context.EnvSpecificInfo
		remotePort = env.GetDebugPort()

		o.componentName = env.GetName()
		o.Namespace = env.GetNamespace()

	} else {
		// this populates the LocalConfigInfo
		o.Context = genericclioptions.NewContext(cmd)

		// a small shortcut
		cfg := o.Context.LocalConfigInfo
		remotePort = cfg.GetDebugPort()

		o.componentName = cfg.GetName()
		o.applicationName = cfg.GetApplication()
		o.Namespace = cfg.GetProject()
	}

	// try to listen on the given local port and check if the port is free or not
	addressLook := "localhost:" + strconv.Itoa(o.localPort)
	listener, err := net.Listen("tcp", addressLook)
	if err != nil {
		// if the local-port flag is set by the user, return the error and stop execution
		flag := cmd.Flags().Lookup("local-port")
		if flag != nil && flag.Changed {
			return err
		}
		// else display a error message and auto select a new free port
		log.Errorf("the local debug port %v is not free, cause: %v", o.localPort, err)
		o.localPort, err = util.HttpGetFreePort()
		if err != nil {
			return err
		}
		log.Infof("The local port %v is auto selected", o.localPort)
	} else {
		err = listener.Close()
		if err != nil {
			return err
		}
	}

	o.PortPair = fmt.Sprintf("%d:%d", o.localPort, remotePort)

	// Using Discard streams because nothing important is logged
	o.PortForwarder = debug.NewDefaultPortForwarder(o.componentName, o.applicationName, o.Namespace, o.Client, o.KClient, k8sgenclioptions.NewTestIOStreamsDiscard())

	o.StopChannel = make(chan struct{}, 1)
	o.ReadyChannel = make(chan struct{})
	return err
}

// Validate validates all the required options for port-forward cmd.
func (o PortForwardOptions) Validate() error {

	if len(o.PortPair) < 1 {
		return fmt.Errorf("ports cannot be empty")
	}
	return nil
}

// Run implements all the necessary functionality for port-forward cmd.
func (o PortForwardOptions) Run() error {

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt,
		syscall.SIGHUP,
		syscall.SIGINT,
		syscall.SIGTERM,
		syscall.SIGQUIT)
	defer signal.Stop(signals)
	defer os.RemoveAll(debug.GetDebugInfoFilePath(o.componentName, o.applicationName, o.Namespace))

	go func() {
		<-signals
		if o.StopChannel != nil {
			close(o.StopChannel)
		}
	}()

	err := debug.CreateDebugInfoFile(o.PortForwarder, o.PortPair)
	if err != nil {
		return err
	}

	return o.PortForwarder.ForwardPorts(o.PortPair, o.StopChannel, o.ReadyChannel, o.isExperimental)
}

// NewCmdPortForward implements the port-forward odo command
func NewCmdPortForward(name, fullName string) *cobra.Command {

	opts := NewPortForwardOptions()
	cmd := &cobra.Command{
		Use:     name,
		Short:   "Forward one or more local ports to a pod",
		Long:    portforwardLong,
		Example: portforwardExample,
		Run: func(cmd *cobra.Command, args []string) {
			genericclioptions.GenericRun(opts, cmd, args)
		},
	}
	genericclioptions.AddContextFlag(cmd, &opts.contextDir)
	if experimental.IsExperimentalModeEnabled() {
		cmd.Flags().StringVar(&opts.DevfilePath, "devfile", "./devfile.yaml", "Path to a devfile.yaml")
	}
	cmd.Flags().IntVarP(&opts.localPort, "local-port", "l", config.DefaultDebugPort, "Set the local port")

	return cmd
}
