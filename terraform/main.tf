# Provisionnement OCI seulement : réseau, instances, stockage objet, IAM.
# La configuration des machines appartient à Ansible. La frontière est nette :
# Terraform crée ce qui a une API chez le fournisseur, Ansible configure ce qui
# tourne dedans. Rien d'autre ne traverse.

terraform {
  required_version = ">= 1.9"
  required_providers {
    oci = { source = "oracle/oci", version = "~> 6.0" }
  }
  # L'état vit dans le bucket, pas sur le portable : le perdre laisse des
  # ressources orphelines facturables qu'on ne retrouve qu'à la facture.
  backend "s3" {
    key                         = "caffelatte.tfstate"
    region                      = "ca-montreal-1"
    skip_region_validation      = true
    skip_credentials_validation = true
    skip_requesting_account_id  = true
    use_path_style              = true
  }
}

resource "oci_core_vcn" "main" {
  compartment_id = var.compartment_id
  cidr_blocks    = ["10.0.0.0/16"]
  display_name   = "caffelatte"
  dns_label      = "caffelatte"
}

resource "oci_core_internet_gateway" "main" {
  compartment_id = var.compartment_id
  vcn_id         = oci_core_vcn.main.id
}

resource "oci_core_route_table" "main" {
  compartment_id = var.compartment_id
  vcn_id         = oci_core_vcn.main.id
  route_rules {
    destination       = "0.0.0.0/0"
    network_entity_id = oci_core_internet_gateway.main.id
  }
}

# La Security List est une couche SÉPARÉE de firewalld sur l'hôte. Les deux
# doivent autoriser le port. Un port ouvert d'un seul côté donne un timeout
# silencieux — c'est la perte de temps numéro un sur OCI.
resource "oci_core_security_list" "public" {
  compartment_id = var.compartment_id
  vcn_id         = oci_core_vcn.main.id

  egress_security_rules {
    destination = "0.0.0.0/0"
    protocol    = "all"
  }

  dynamic "ingress_security_rules" {
    for_each = var.public_tcp_ports
    content {
      protocol = "6" # TCP
      source   = "0.0.0.0/0"
      tcp_options {
        min = ingress_security_rules.value
        max = ingress_security_rules.value
      }
    }
  }

  ingress_security_rules {
    protocol = "17" # UDP — Bedrock
    source   = "0.0.0.0/0"
    udp_options {
      min = 19132
      max = 19132
    }
  }
  # SSH n'est PAS ici : l'accès passe par OCI Bastion, donc aucun port 22 exposé.
}

resource "oci_core_subnet" "public" {
  compartment_id    = var.compartment_id
  vcn_id            = oci_core_vcn.main.id
  cidr_block        = "10.0.1.0/24"
  route_table_id    = oci_core_route_table.main.id
  security_list_ids = [oci_core_security_list.public.id]
  dns_label         = "public"
}

# 2 OCPU / 12 Go : plafond Always Free depuis le 2026-06-15. Demander plus
# échoue en LimitExceeded, ce qui ressemble à tort à un manque de capacité.
resource "oci_core_instance" "core" {
  compartment_id      = var.compartment_id
  availability_domain = var.availability_domain # ca-montreal-1 n'en a qu'un
  shape               = "VM.Standard.A1.Flex"
  display_name        = "core"

  shape_config {
    ocpus         = 2
    memory_in_gbs = 12
  }

  source_details {
    source_type             = "image"
    source_id               = var.arm_image_id
    boot_volume_size_in_gbs = 100
  }

  create_vnic_details {
    subnet_id = oci_core_subnet.public.id
  }

  metadata = {
    ssh_authorized_keys = var.ssh_public_key
  }

  # La capacité ARM à Montréal est intermittente. `terraform apply` échoue en
  # « Out of capacity » ; il suffit de relancer. Voir docs/runbook-capacite-arm.md.
  lifecycle {
    ignore_changes = [source_details[0].source_id] # pas de recréation sur nouvelle image
  }
}

resource "oci_objectstorage_bucket" "backup" {
  compartment_id = var.compartment_id
  namespace      = var.os_namespace
  name           = "caffelatte-backup"
  access_type    = "NoPublicAccess"
  versioning     = "Enabled"
}
