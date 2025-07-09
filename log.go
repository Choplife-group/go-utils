package library

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"
)

type LogConfig struct {
	ResourceTypeMap map[string]string
	Publisher       Publisher
}

type Publisher interface {
	Publish(ctx context.Context, routingKey string, data interface{}, priority uint8) error
}

type LogQueueData struct {
	ProfileID    int64  `json:"profile_id"`
	Description  string `json:"description"`
	Method       int16  `json:"method" enums:"1,2,3,4,5"`
	ResourceId   int64  `json:"resource_id"`
	ResourceType string `json:"resource_type"`
	IpAddress    string `json:"ip_address"`
}

func LoggingMiddleware(config LogConfig) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			err := next(c)
	
			if c.Response().Status < 300 {
				go func() {
					profileID := GetProfileIDFromContext(c)
	
					resourceType, resourceID := ExtractResourceInfo(c, config.ResourceTypeMap)
	
					
					logData := LogQueueData{
						ProfileID:    profileID,
						Description:  GenerateDescription(c.Request().Method, c.Path()),
						Method:       GetMethodCode(c.Request().Method),
						ResourceId:   resourceID,
						ResourceType: resourceType,
						IpAddress:    c.RealIP(),
					}

					_ = config.Publisher.Publish(context.Background(), "logging-service.log", logData, 0)
				}()
			}
			
			return err
		}
	}
}

func GetMethodCode(method string) int16 {
	switch strings.ToUpper(method) {
	case "GET":
		return METHOD_GET
	case "POST":
		return METHOD_POST
	case "PUT":
		return METHOD_PUT
	case "DELETE":
		return METHOD_DELETE
	case "PATCH":
		return METHOD_PATCH
	default:
		return METHOD_GET
	}
}

func GenerateDescription(method, path string) string {
	action := ""
	switch strings.ToUpper(method) {
	case "GET":
		action = ACTION_GET
	case "POST":
		action = ACTION_POST
	case "PUT":
		action = ACTION_PUT
	case "DELETE":
		action = ACTION_DELETE
	case "PATCH":
		action = ACTION_PATCH
	default:
		action = ACTION_GET
	}
	
	resource := strings.ReplaceAll(strings.Trim(path, "/"), "/", " ")
	resource = strings.ReplaceAll(resource, "-", " ")

	if strings.Contains(resource, ":") {
		parts := strings.Split(resource, " ")
		cleanParts := []string{}
		for _, part := range parts {
			if !strings.HasPrefix(part, ":") {
				cleanParts = append(cleanParts, part)
			}
		}
		resource = strings.Join(cleanParts, " ")
	}
	
	return fmt.Sprintf("User %s %s", action, resource)
}

func GetProfileIDFromContext(c echo.Context) int64 {
	_, profileID, _, _, _, err := GetSessionValues(c)
	if err != nil {
		return 0
	}

	return profileID
}

func ExtractResourceInfo(c echo.Context, resourceTypeMap map[string]string) (string, int64) {
	path := c.Path()
	
	basePath := strings.Split(path, "/:")[0]
	
	resourceType := resourceTypeMap[basePath]
	if resourceType == "" {
		pathParts := strings.Split(strings.Trim(path, "/"), "/")
		if len(pathParts) > 0 {
			resourceType = strings.ReplaceAll(pathParts[0], "-", "_")
		}
	}
	
	var resourceID int64
	if idParam := c.Param("id"); idParam != "" {
		if parsed, err := strconv.ParseInt(idParam, 10, 64); err == nil {
			resourceID = parsed
		}
	}
	
	return resourceType, resourceID
}