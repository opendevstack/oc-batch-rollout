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
	projectv1 "github.com/openshift/client-go/project/clientset/versioned/typed/project/v1"
)

const (
	deployRunningThreshold     = time.Minute * 5
	deployRunningCheckInterval = time.Second * 5
)

func main() {
	fs := flag.NewFlagSet("obr", flag.ExitOnError)
	var (
		host          = fs.String("host", "", "host")
		token         = fs.String("token", "", "token")
		projectsRegex = fs.String("projects", "", "regex filter for projects")
		deployment    = fs.String("deployment", "", "name of deployment configs")
		currentImage  = fs.String("current-image", "", "current image sha or tag")
		newImage      = fs.String("new-image", "", "new image sha or tag")
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

	ff.Parse(fs, os.Args[1:],
		ff.WithConfigFileFlag("config"),
		ff.WithConfigFileParser(ff.PlainParser),
		ff.WithEnvVarPrefix("WAVE"),
	)

	err := run(*host, *token, *projectsRegex, *deployment, *currentImage, *newImage, *kubeconfig, *batchSize)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

type targetDeployment struct {
	project string
	name    string
}

func run(host string, token string, projectsRegex string, deployment string, currentImage string, newImage string, kubeconfig string, batchSize int) error {
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

	imageFilter := "having any image"
	if len(currentImage) > 0 {
		imageFilter = fmt.Sprintf("having image \"%s\"", currentImage)
	}
	fmt.Printf(
		"Rolling out image \"%s\" to all deployments named \"%s\" (%s) in projects matching \"%s\".\n\n",
		newImage,
		deployment,
		imageFilter,
		projectsRegex,
	)

	projectV1Client, err := projectv1.NewForConfig(config)
	if err != nil {
		return err
	}

	projects, err := projectV1Client.Projects().List(metav1.ListOptions{})
	if err != nil {
		return err
	}
	fmt.Printf("Found %d projects in total.\n", len(projects.Items))

	// Filter to matching projects
	allowedProjects, err := regexp.Compile(projectsRegex)
	if err != nil {
		return fmt.Errorf("Argument to --projects (%s) is not a valid regex expression: %s", projectsRegex, err)
	}
	targetProjects := []string{}
	for _, p := range projects.Items {
		if allowedProjects.MatchString(p.Name) {
			targetProjects = append(targetProjects, p.Name)
		}
	}
	fmt.Printf("Found %d projects matching '%s'.\n\n", len(targetProjects), projectsRegex)
	if len(targetProjects) < 1 {
		return nil
	}

	imageV1Client, err := imagev1.NewForConfig(config)
	if err != nil {
		return err
	}

	// Ensure the passed image tag is resolvable
	if len(newImage) == 0 {
		fmt.Println("--new-image is required")
		os.Exit(1)
	}
	newImageReference, err := getImageReference(imageV1Client, newImage)
	if err != nil {
		return err
	}
	fmt.Printf("Found new image tag %s. It references:\n%s\n", newImage, newImageReference)

	// Get current image reference to be able to compare it with
	// the SHA of the deploymentconfig
	currentImageReference := ""
	if len(currentImage) > 0 {
		currentImageReference, err = getImageReference(imageV1Client, currentImage)
		if err != nil {
			return err
		}
		fmt.Printf("Found current image tag '%s', which resolves to: %s.\n", currentImage, currentImageReference)
	}

	fmt.Println("")
	ok := askForConfirmation("Do you want to continue?")
	if !ok {
		os.Exit(0)
	}

	appsV1Client, err := appsv1.NewForConfig(config)
	if err != nil {
		return err
	}

	fmt.Print("\nSearching for matching deployment configs ...\n\n")
	targetDeployments := []targetDeployment{}

	for i, p := range targetProjects {
		deploymentsClient := appsV1Client.DeploymentConfigs(p)
		fmt.Printf("Checking for %s in project %s", deployment, p)
		if len(currentImageReference) > 0 {
			fmt.Printf("(having image %s)", currentImage)
		}
		fmt.Print(" ... ")
		dc, err := deploymentsClient.Get(deployment, metav1.GetOptions{})
		if err != nil {
			fmt.Println("not found.")
		} else {
			isCandidate := false
			if len(currentImageReference) > 0 {
				dcImage := dc.Spec.Template.Spec.Containers[0].Image
				if dcImage == currentImageReference {
					isCandidate = true
				} else {
					fmt.Println("not matching current image.")
					isCandidate = false
				}
			} else {
				isCandidate = true
			}
			// Ensure image is not already set to newImageReference.
			if isCandidate && dc.Spec.Template.Spec.Containers[0].Image == newImageReference {
				fmt.Println("already at new image.")
				isCandidate = false
			}
			if isCandidate {
				fmt.Println("found.")
				targetDeployments = append(targetDeployments, targetDeployment{
					project: p,
					name:    dc.Name,
				})
			}
		}

		if len(targetDeployments) == batchSize || (len(targetDeployments) > 0 && i == len(targetProjects)-1) {
			fmt.Printf("\nUpdating %d deployments in one batch ...\n", len(targetDeployments))
			var wg sync.WaitGroup
			for _, d := range targetDeployments {
				wg.Add(1)
				updateWorker(clientSet, appsV1Client, d, newImage, newImageReference, &wg)
			}
			wg.Wait()
			targetDeployments = []targetDeployment{}
		}
	}

	fmt.Println("\nDone.")

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

func updateWorker(clientSet *kubernetes.Clientset, appsV1Client *appsv1.AppsV1Client, target targetDeployment, newImage string, newImageReference string, wg *sync.WaitGroup) {
	defer wg.Done()

	deploymentsClient := appsV1Client.DeploymentConfigs(target.project)
	var previousVersion int64

	retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		dc, getErr := deploymentsClient.Get(target.name, metav1.GetOptions{})
		if getErr != nil {
			return getErr
		}
		previousVersion = dc.Status.LatestVersion
		if dc.Spec.Template.Spec.Containers[0].Image == newImageReference {
			return fmt.Errorf("%s has been updated to %s", target.name, newImage)
		}
		imageTriggerExists := false
		triggerIndex := 0
		for i, t := range dc.Spec.Triggers {
			if t.Type == v1.DeploymentTriggerOnImageChange {
				imageTriggerExists = true
				triggerIndex = i
				break
			}
		}

		if imageTriggerExists {
			imageParts := strings.Split(newImage, "/")
			imageNamespace := imageParts[0]
			imageName := imageParts[1]
			dc.Spec.Triggers[triggerIndex].ImageChangeParams.From.Namespace = imageNamespace
			dc.Spec.Triggers[triggerIndex].ImageChangeParams.From.Name = imageName
		} else {
			dc.Spec.Template.Spec.Containers[0].Image = newImageReference
		}
		_, updateErr := deploymentsClient.Update(dc)
		return updateErr
	})
	if retryErr != nil {
		fmt.Println(retryErr)
		return
	}

	err := waitForAvailableReplicas(deploymentsClient, target.name, previousVersion)
	if err != nil {
		fmt.Println(err)
		return
	}

	fmt.Print("✔")
}

func waitForAvailableReplicas(deploymentsClient appsv1.DeploymentConfigInterface, name string, previousVersion int64) error {
	end := time.Now().Add(deployRunningThreshold)

	for true {
		<-time.NewTimer(deployRunningCheckInterval).C

		var err error
		running, err := availableReplicas(deploymentsClient, name, previousVersion)
		if running {
			return nil
		}

		if err != nil {
			return fmt.Errorf("Encountered an error checking for running pods: %s", err)
		}

		if time.Now().After(end) {
			return fmt.Errorf("Failed to get available replicas within timeout")
		}
	}
	return nil
}

func availableReplicas(deploymentsClient appsv1.DeploymentConfigInterface, name string, previousVersion int64) (bool, error) {
	fmt.Print(".")
	dc, err := deploymentsClient.Get(name, metav1.GetOptions{})
	if err != nil {
		return false, err
	}
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
