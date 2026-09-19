# L'IPv4 publique sort directement de l'instance, mais PAS l'IPv6 : il faut
# descendre au VNIC. C'est le seul endroit où l'adresse v6 est lisible, et
# c'est elle que le futur enregistrement AAAA consommera.
data "oci_core_vnic_attachments" "core" {
  compartment_id = var.compartment_id
  instance_id    = oci_core_instance.core.id
}

data "oci_core_vnic" "core" {
  vnic_id = data.oci_core_vnic_attachments.core.vnic_attachments[0].vnic_id
}

output "ipv4_publique" {
  description = "IPv4 ÉPHÉMÈRE : peut changer si l'instance est reconstruite."
  value       = oci_core_instance.core.public_ip
}

output "ipv6_publique" {
  description = "IPv6 issue du préfixe du VCN : stable tant que le VCN existe."
  value       = try(data.oci_core_vnic.core.ipv6addresses[0], null)
}

output "vcn_ipv6_cidr" {
  description = "Le /56 alloué par Oracle. Le sous-réseau en prend le premier /64."
  value       = oci_core_vcn.main.ipv6cidr_blocks
}
