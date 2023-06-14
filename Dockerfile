FROM golang:1.20.5-alpine AS build
WORKDIR src/
COPY go.mod go.sum .
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /aws-in-a-box


FROM scratch
COPY --from=build /aws-in-a-box /aws-in-a-box
ENTRYPOINT ["/aws-in-a-box"]
