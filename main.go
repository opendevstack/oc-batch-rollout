package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/peterbourgon/ff/v2"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/retry"

	v1 "github.com/openshift/api/apps/v1"
	appsv1 "github.com/openshift/client-go/apps/clientset/versioned/typed/apps/v1"
	imagev1 "github.com/openshift/client-go/image/clientset/versioned/typed/image/v1"
)

const (
	deployRunningTimeout       = time.Second * 300
	deployRunningCheckInterval = time.Second * 5
)

func main() {
	fs := flag.NewFlagSet("obr", flag.ExitOnError)
	var (
		host          = fs.String("host", "", "host")
		token         = fs.String("token", "", "token")
		projectsRegex = fs.String("projects", "", "regex filter for projects")
		deployment    = fs.String("deployment", "", "name of deployment configs")
		currentImage  = fs.String("current-image", "", "current image tag (foo/bar:baz) or SHA (registry.example.com/foo/bar@sha256:06c...e6b)")
		newImage      = fs.String("new-image", "", "new image sha tag (foo/bar:baz) or SHA (registry.example.com/foo/bar@sha256:06c...e6b)")
		batchSize     = fs.Int("batchsize", 10, "number of simultaneous rollouts")
		_             = fs.String("config", "", "config file (optional)")
	)

	var kubeconfig *string
	homeDir, _ := os.UserHomeDir()
	if homeDir != "" {
		kubeconfig = fs.String("kubeconfig", filepath.Join(homeDir, ".kube", "config"), "(optional) absolute path to the kubeconfig file")
	} else {
		kubeconfig = fs.String("kubeconfig", "", "absolute path to the kubeconfig file")
	}

	err := ff.Parse(fs, os.Args[1:],
		ff.WithConfigFileFlag("config"),
		ff.WithConfigFileParser(ff.PlainParser),
		ff.WithEnvVarPrefix("OBR"),
	)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	if newImage == nil {
		fmt.Println("--new-image is required")
		os.Exit(1)
	}

	err = run(*host, *token, *projectsRegex, *deployment, *currentImage, *newImage, *kubeconfig, *batchSize)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

type targetDeployment struct {
	project       string
	name          string
	deployedImage string
	newImage      string
}

func run(host string, token string, projectsRegex string, deployment string, currentImageFlag string, newImageFlag string, kubeconfig string, batchSize int) error {
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		return err
	}

	if len(host) > 0 && len(token) > 0 {
		config.Host = host
		config.BearerToken = token
	} else if len(kubeconfig) == 0 {
		return errors.New("You must configure either kubeconfig or host/token")
	}

	clientSet, err := kubernetes.NewForConfig(config)
	if err != nil {
		panic(err)
	}

	// Filter to matching projects
	allowedProjects, err := regexp.Compile(projectsRegex)
	if err != nil {
		return fmt.Errorf("Argument to --projects (%s) is not a valid regex expression: %s", projectsRegex, err)
	}

	imageV1Client, err := imagev1.NewForConfig(config)
	if err != nil {
		return err
	}

	// Ensure the passed image tag is resolvable
	var newImageReference string
	newImage := ""
	if strings.Contains(newImageFlag, "@sha") {
		newImageReference = newImageFlag
		newImage = newImageFlag
	} else {
		i, err := getImageReference(imageV1Client, newImageFlag)
		if err != nil {
			return err
		}
		newImageReference = i
		newImageRegistry := strings.Split(i, "/")[0]
		newImage = newImageRegistry + "/" + newImageFlag
		fmt.Printf("\nFound new image tag '%s', which resolves to:\n%s\n\n", newImageFlag, i)
	}

	// Get current image reference to be able to compare it with
	// the SHA of the deploymentconfig
	currentImage := ""
	if len(currentImageFlag) > 0 {
		if strings.Contains(currentImageFlag, "@sha") {
			currentImage = currentImageFlag
		} else {
			i, err := getImageReference(imageV1Client, currentImageFlag)
			if err != nil {
				return err
			}
			fmt.Printf("Found current image tag '%s', which resolves to:\n%s\n\n", currentImageFlag, i)
			currentImageRegistry := strings.Split(i, "/")[0]
			currentImage = currentImageRegistry + "/" + currentImageFlag
		}
	}

	targetDeployments := []targetDeployment{}
	fmt.Printf("Searching for deployment configs named '%s' in '%s' with image %s ...\n\n", deployment, projectsRegex, currentImage)
	podList, err := clientSet.Core().Pods("").List(metav1.ListOptions{LabelSelector: "deploymentconfig=" + deployment})
	if err != nil {
		panic(err)
	}
	namespaceMismatch := 0
	currentImageMismatch := 0
	alreadyAtNewImage := 0
	for _, v := range podList.Items {
		if allowedProjects.MatchString(v.Namespace) {

			if currentImage == v.Spec.Containers[0].Image {
				deployedImage := strings.Split(v.Status.ContainerStatuses[0].ImageID, "://")[1]
				if deployedImage == newImageReference && v.Spec.Containers[0].Image == newImage {
					alreadyAtNewImage++
				} else {
					targetDeployments = append(targetDeployments, targetDeployment{
						project:       v.Namespace,
						name:          deployment,
						deployedImage: deployedImage,
						newImage:      newImage,
					})
				}
			} else {
				currentImageMismatch++
			}
		} else {
			namespaceMismatch++
		}
	}

	msgDeployments := "deployments"
	if len(podList.Items) == 1 {
		msgDeployments = "deployment"
	}
	fmt.Printf("Found %d %s of '%s' across all projects.\n", len(podList.Items), msgDeployments, deployment)
	if namespaceMismatch > 0 {
		msg := "projects do not match"
		if namespaceMismatch == 1 {
			msg = "project does not match"
		}
		fmt.Printf("- %d %s '%s'\n", namespaceMismatch, msg, projectsRegex)
	}
	if currentImageMismatch > 0 {
		msg := "deployments are not using image"
		if currentImageMismatch == 1 {
			msg = "deployment is not using image"
		}
		fmt.Printf("- %d %s '%s'\n", currentImageMismatch, msg, currentImage)
	}
	if alreadyAtNewImage > 0 {
		msg := "deployments are already using new image"
		if currentImageMismatch == 1 {
			msg = "deployment is already using new image"
		}
		fmt.Printf("- %d %s '%s'\n", alreadyAtNewImage, msg, newImageReference)
	}

	if len(targetDeployments) == 0 {
		fmt.Println("\nNo deployments selected for update.")
		os.Exit(0)
	}

	msg := "deployments for update"
	if len(targetDeployments) == 1 {
		msg = "deployment for update"
	}
	fmt.Printf("\nSelected %d %s:\n", len(targetDeployments), msg)
	for _, v := range targetDeployments {
		if len(currentImageFlag) > 0 {
			fmt.Printf("- %s/%s\n", v.project, v.name)
		} else {
			fmt.Printf("- %s/%s (using %s)\n", v.project, v.name, v.deployedImage)
		}

	}

	fmt.Println("")
	ok := askForConfirmation(fmt.Sprintf("Do you want to start rollout (%d in parallel)?", batchSize))
	if !ok {
		os.Exit(0)
	}

	var batched [][]targetDeployment

	chunkSize := (len(targetDeployments) + batchSize - 1) / batchSize

	for i := 0; i < len(targetDeployments); i += chunkSize {
		end := i + chunkSize

		if end > len(targetDeployments) {
			end = len(targetDeployments)
		}

		batched = append(batched, targetDeployments[i:end])
	}

	appsV1Client, err := appsv1.NewForConfig(config)
	if err != nil {
		return err
	}

	for _, v := range batched {
		fmt.Printf("\nUpdating %d deployments in parallel ...\n", len(batched))
		var wg sync.WaitGroup
		for _, d := range v {
			wg.Add(1)
			go updateWorker(clientSet, appsV1Client, d, &wg)
		}
		wg.Wait()
	}

	fmt.Println("\n\nDone.")

	return nil
}

func getImageReference(imageV1Client *imagev1.ImageV1Client, imageName string) (string, error) {
	imageNameParts := strings.Split(imageName, "/")
	if len(imageNameParts) < 2 {
		return "", fmt.Errorf("Must be namespace/image:tag, got only %s", imageName)
	}
	imageNameNamespace := imageNameParts[0]
	imageNameTag := imageNameParts[1]
	if !strings.Contains(imageNameTag, ":") {
		return "", fmt.Errorf("Must be namespace/image:tag, got only %s", imageName)
	}
	is, err := imageV1Client.ImageStreamTags(imageNameNamespace).Get(imageNameTag, metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	return is.Image.DockerImageReference, nil
}

func updateWorker(clientSet *kubernetes.Clientset, appsV1Client *appsv1.AppsV1Client, target targetDeployment, wg *sync.WaitGroup) {
	defer wg.Done()

	deploymentsClient := appsV1Client.DeploymentConfigs(target.project)
	var previousVersion int64

	retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		dc, getErr := deploymentsClient.Get(target.name, metav1.GetOptions{})
		if getErr != nil {
			return getErr
		}
		previousVersion = dc.Status.LatestVersion
		configTriggerExists := false
		for _, t := range dc.Spec.Triggers {
			if t.Type == v1.DeploymentTriggerOnImageChange {
				return fmt.Errorf("%s uses an image trigger which is not supported", target.name)
			} else if t.Type == v1.DeploymentTriggerOnConfigChange {
				configTriggerExists = true
			}
		}

		if dc.Spec.Template.Spec.Containers[0].Image != target.newImage {
			dc.Spec.Template.Spec.Containers[0].Image = target.newImage
			_, updateErr := deploymentsClient.Update(dc)
			if updateErr != nil {
				return updateErr
			}
			if configTriggerExists {
				return nil
			}
		}
		_, instantiateErr := deploymentsClient.Instantiate(target.name, &v1.DeploymentRequest{Name: target.name, Force: true})
		return instantiateErr

	})
	if retryErr != nil {
		fmt.Printf("Could not rollout %s/%s: %s\n", target.project, target.name, retryErr)
		return
	}

	waitErr := waitForAvailableReplicas(deploymentsClient, target.name, previousVersion)
	if waitErr != nil {
		fmt.Printf("Failed to observe new available replicas for %s/%s: %s\n", target.project, target.name, waitErr)
		return
	}

	fmt.Print("✔")
}

func waitForAvailableReplicas(deploymentsClient appsv1.DeploymentConfigInterface, name string, previousVersion int64) error {
	end := time.Now().Add(deployRunningTimeout)

	fmt.Print(".")

	for {
		<-time.NewTimer(deployRunningCheckInterval).C

		var err error
		running, err := availableReplicas(deploymentsClient, name, previousVersion)
		if err != nil {
			return fmt.Errorf("Encountered an error checking for running pods: %s", err)
		}

		if running {
			return nil
		}

		if time.Now().After(end) {
			return fmt.Errorf("Failed to get available replicas within timeout")
		}
	}
}

func availableReplicas(deploymentsClient appsv1.DeploymentConfigInterface, name string, previousVersion int64) (bool, error) {
	fmt.Print(".")
	dc, err := deploymentsClient.Get(name, metav1.GetOptions{})
	if err != nil {
		return false, err
	}
	// TODO: Ensure that the version has been increased, otherwise we wait in vain.
	return dc.Status.LatestVersion > previousVersion && dc.Status.ReadyReplicas > 0, nil
}

// askForConfirmation asks the user for confirmation. A user must type in "yes" or "no" and
// then press enter. It has fuzzy matching, so "y", "Y", "yes", "YES", and "Yes" all count as
// confirmations. If the input is not recognized, it will ask again. The function does not return
// until it gets a valid response from the user.
func askForConfirmation(s string) bool {
	reader := bufio.NewReader(os.Stdin)

	for {
		fmt.Printf("%s [y/n]: ", s)

		response, err := reader.ReadString('\n')
		if err != nil {
			log.Fatal(err)
		}

		response = strings.ToLower(strings.TrimSpace(response))

		if response == "y" || response == "yes" {
			return true
		} else if response == "n" || response == "no" {
			return false
		}
	}
}
