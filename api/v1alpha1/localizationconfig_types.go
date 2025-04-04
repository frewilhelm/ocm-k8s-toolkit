package v1alpha1

import (
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const KindLocalizationConfig = "LocalizationConfig"

// +kubebuilder:object:root=true

// LocalizationConfig defines a description of a localization.
// It contains the necessary localization rules that can be used in conjunction with a data source to localize resources.
type LocalizationConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              LocalizationConfigSpec `json:"spec"`
}

// +kubebuilder:object:root=true

// LocalizationConfigList contains a list of LocalizationConfig.
type LocalizationConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []LocalizationConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&LocalizationConfig{}, &LocalizationConfigList{})
}

func (in *LocalizationConfig) GetRules() []LocalizationRule {
	return in.Spec.Rules
}

// LocalizationConfigSpec defines the desired state of LocalizationConfig.
// It contains the necessary localization rules that can be used in conjunction with a data source to localize resources.
// For more information, see the LocalizationRule type.
type LocalizationConfigSpec struct {
	// +kubebuilder:validation:MinItems=1
	Rules []LocalizationRule `json:"rules"`
}

// LocalizationRule defines a rule that can be used to localize resources.
// +kubebuilder:validation:MinProperties=1
// +kubebuilder:validation:MaxProperties=1
type LocalizationRule struct {
	YAMLSubstitution *LocalizationRuleYAMLSubstitution `json:"yamlsubst,omitempty"`
	GoTemplate       *LocalizationRuleGoTemplate       `json:"goTemplate,omitempty"`
}

// LocalizationRuleYAMLSubstitution is a rule that can be used to localize resources based on a mapping from resources to paths.
//
// Example:
//
//	map:
//	  resource:
//	    name: image
//	  file:
//	    path: values.yaml
//	    value: deploy.image
//	  transformation:
//	    type: Repository
//
// For more information on the type, see the subtypes for Resource, FileTargetWithValue and Transformation.
type LocalizationRuleYAMLSubstitution struct {
	Source LocalizationRuleYAMLSubstitutionSource `json:"source"`
	Target LocalizationRuleYAMLSubstitutionTarget `json:"target"`
	// The Transformation is used to tell the rule additional information about how to transform the content.
	// The transformation can be used to digest the source in a different way or interpret the rule differently.
	// A simple example of this is the TransformationTypeRepository,
	// which extracts the registry portion of an image reference:
	//
	// Example:
	//   map:
	//     transformation:
	//       type: Repository
	//
	// The default TransformationType is TransformationTypeImage, which extracts the full image reference.
	// For more information on individual TransformationType's, see their respective documentation.
	Transformation Transformation `json:"transformation,omitempty"`
}

type LocalizationRuleYAMLSubstitutionSource struct {
	// The Resource reference is used to identify the resource that will be used to fill in the target reference.
	// If one has a ComponentDescriptor with 2 resources, one can use this to reference between them.
	// For a Component Descriptor with two resources, a "deployment-instruction" (to be localized)
	// and an "image" (to be localized from), one can use the following source:
	//
	//   map:
	//     source:
	//       resource: image
	//
	// The localization will then look into the corresponding descriptor and resolve its AccessType:
	//   components:
	//    - component:
	//        # ...
	//        resources:
	//          - access:
	//              imageReference: ghcr.io/stefanprodan/podinfo:6.2.0
	//              type: ociArtifact
	//            name: image
	//            relation: external
	//            type: ociImage
	//            version: 6.2.0
	//        sources: []
	//        version: 1.0.0
	//      meta:
	//        schemaVersion: v2
	//
	// This would then lead to a value of "ghcr.io/stefanprodan/podinfo:6.2.0".
	ResourceReference `json:",inline"`
}

type LocalizationRuleYAMLSubstitutionTarget struct {
	// File is used to identify the file where the rule will apply its data to after considering
	// the transformation.
	File FileTargetWithValue `json:"file"`
}

// LocalizationRuleGoTemplate is a rule that can be used to localize resources based on a goTemplate applied to a file.
//
// Example:
//
//	goTemplate:
//	  file:
//	    path: values.yaml
//	  data:
//	    key: value
//	  delimiters:
//	    left: "ocm{{"
//	    right: "}}ocm"
//
// For more information on the YAMLSubstitution type, see the subtypes for Resource, FileTargetWithValue and Transformation.
type LocalizationRuleGoTemplate struct {
	// FileTarget is used to identify the file where the rule will apply its data to (parse the GoTemplate)
	FileTarget FileTarget `json:"file"`

	// GoTemplateData is an arbitrary object that is forwarded to the GoTemplate for use as a struct.
	//
	// Example:
	//
	//	goTemplate:
	//	  data:
	//	    key: value
	//
	// This would then lead to a struct that can be used in the GoTemplate (assuming standard Delimiters):
	//
	//	{{ .key }}
	Data *apiextensionsv1.JSON `json:"data,omitempty"`

	Delimiters *GoTemplateDelimiters `json:"delimiters,omitempty"`
}

// FileTargetWithValue describes a value within a file inside a Resource.
// It can contain a path and a value, where Path is the filepath (relative to the Resource) to the file inside the resource
// and the value is a reference to the content that should be localized.
// If one wants to store the image fetched from source into a file called values.yaml inside deploy.image,
// one can use the following value:
//
//	file:
//	  value: deploy.image
//	  path: values.yaml
type FileTargetWithValue struct {
	FileTarget `json:",inline"`
	// The Value is a reference to the content that should be localized.
	Value string `json:"value"`
}

// FileTarget is used to identify the file where the rule will apply its data to.
// If one wants to reference a file called values.yaml inside a resource, one can use the following value:
//
//	file:
//	  path: values.yaml
type FileTarget struct {
	// The Path is the filepath (relative to the Resource) to the file inside the resource.
	Path string `json:"path"`
}

type Transformation struct {
	//+kubebuilder:default=Image
	Type TransformationType `json:"type"`
}

type TransformationType string

const (
	// TransformationTypeImage is a transformation that extracts the full image reference.
	// This is the default transformation type.
	// Example:
	//   "docker.io/library/nginx:latest" -> "docker.io/library/nginx:latest".
	TransformationTypeImage TransformationType = "Image"

	// TransformationTypeImageNoTag is a transformation that extracts the image reference without the tag.
	// Example:
	//   "docker.io/library/nginx:latest" -> "docker.io/library/nginx".
	TransformationTypeImageNoTag TransformationType = "ImageNoTag"

	// TransformationTypeRegistry is a transformation that extracts the registry part of an image reference.
	// Example:
	//   "docker.io/library/nginx:latest" -> "docker.io".
	TransformationTypeRegistry TransformationType = "Registry"

	// TransformationTypeRepository is a transformation that extracts the repository part of an image reference.
	// Example:
	//   "docker.io/library/nginx:latest" -> "library/nginx".
	TransformationTypeRepository TransformationType = "Repository"

	// TransformationTypeTag is a transformation that extracts the tag part of an image reference.
	// Example:
	//   "docker.io/library/nginx:latest" -> "latest".
	TransformationTypeTag TransformationType = "Tag"
)

// GoTemplateDelimiters are used to specify the delimiters that are used in the GoTemplate parsing process.
// If one wants to use a different delimiter than the default "{{" and "}}", one can specify them here.
type GoTemplateDelimiters struct {
	Left  string `json:"left"`
	Right string `json:"right"`
}
