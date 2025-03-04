This package allows use of CosmosDB as a data store. This is considered experimental.

We will CHANGE HOW WE STORE THE DATA HERE AT WILL, THIS IS LIKELY TO CATCH YOU OFF GUARD. AKA DON'T USE THIS UNTIL THIS FILE IS REMOVED, USE SQLite storage.

The main reason is that the CosmosDB Go SDK is incomplete and missing a lot of features like cross partition scanning. This makes doing search very difficult.

You could put everything in a single partition, but that means no horizontal scaling and very limiting compute RUs.

So we don't do that, but we do use a single search key and store search records. This means searches can only be done on a single partition worth of compute at 10K RU and have a storage limit of 20 GiB. This means you have to periodically clean out storage.

It also can not be written atomically, because you cannot write across partition keys in an atomic manner. This would not matter if you could scan across partitions, but as we can't...
