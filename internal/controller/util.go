package controller

import corev1 "k8s.io/api/core/v1"

// mergeLabels returns a new map containing base overlaid with add.
func mergeLabels(base, add map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range base {
		out[k] = v
	}
	for k, v := range add {
		out[k] = v
	}
	return out
}

// upsertContainer overlays c onto a container of the same name in list, or
// appends it. The managed fields (image, command, args, env, workingDir) win;
// other fields from a user-supplied container (resources, volumeMounts, probes)
// are preserved.
func upsertContainer(list []corev1.Container, c corev1.Container) []corev1.Container {
	for i := range list {
		if list[i].Name == c.Name {
			list[i].Image = c.Image
			list[i].Command = c.Command
			list[i].Args = c.Args
			list[i].WorkingDir = c.WorkingDir
			list[i].Env = c.Env
			return list
		}
	}
	return append([]corev1.Container{c}, list...)
}
