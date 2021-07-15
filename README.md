# Helmwebclient
Helm Web Client API is a simple Service which has endpoints to Add/Remove/List/Delete repositories & Install/Uninstall Charts in Kubernetes Cluster using helm library.

# How to build
From the helmwebclient directory run,

go mod init helmwebclient
go mod init
go mod tidy
go build -o helmwebclient

# How to Run locally
From the helmwebclient directory run,

go run helmAPI.go

# How to Run in Docker
Build the Docker Image & Start the container with following commands,

docker build -t helm-web-client .
docker run -p 9090:9090 helm-web-client

# How to Run inside the Kubernetes Cluster
## TODO:
To run inside the k8's cluster, you need to install the helmwebclient as a kubernetes manifest.
Deloy the helmwebclient with below command,
kubectl create ns helmwebclient
kubectl apply -f helmwebclient.yaml
kubectl apply -f serviceaccount.yaml

OR

helm install helmwebclient --namespace helmwebclient -f overridefile

All the values can be overridden.
